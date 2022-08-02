// Copyright (c) 2020 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package netcheck checks the network conditions from the current host.
package netcheck

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/tcnksm/go-httpstat"
	"tailscale.com/derp/derphttp"
	"tailscale.com/envknob"
	"tailscale.com/net/interfaces"
	"tailscale.com/net/netaddr"
	"tailscale.com/net/neterror"
	"tailscale.com/net/netns"
	"tailscale.com/net/portmapper"
	"tailscale.com/net/stun"
	"tailscale.com/syncs"
	"tailscale.com/tailcfg"
	"tailscale.com/types/logger"
	"tailscale.com/types/nettype"
	"tailscale.com/types/opt"
	"tailscale.com/util/clientmetric"
)

// Debugging and experimentation tweakables.
var (
	debugNetcheck = envknob.Bool("TS_DEBUG_NETCHECK")
)

// The various default timeouts for things.
const (
	// overallProbeTimeout is the maximum amount of time netcheck will
	// spend gathering a single report.
	overallProbeTimeout = 5 * time.Second
	// stunTimeout is the maximum amount of time netcheck will spend
	// probing with STUN packets without getting a reply before
	// switching to HTTP probing, on the assumption that outbound UDP
	// is blocked.
	stunProbeTimeout = 3 * time.Second
	// hairpinCheckTimeout is the amount of time we wait for a
	// hairpinned packet to come back.
	hairpinCheckTimeout = 100 * time.Millisecond
	// defaultActiveRetransmitTime is the retransmit interval we use
	// for STUN probes when we're in steady state (not in start-up),
	// but don't have previous latency information for a DERP
	// node. This is a somewhat conservative guess because if we have
	// no data, likely the DERP node is very far away and we have no
	// data because we timed out the last time we probed it.
	defaultActiveRetransmitTime = 200 * time.Millisecond
	// defaultInitialRetransmitTime is the retransmit interval used
	// when netcheck first runs. We have no past context to work with,
	// and we want answers relatively quickly, so it's biased slightly
	// more aggressive than defaultActiveRetransmitTime. A few extra
	// packets at startup is fine.
	defaultInitialRetransmitTime = 100 * time.Millisecond
)

type Report struct {
	UDP         bool // a UDP STUN round trip completed
	IPv6        bool // an IPv6 STUN round trip completed
	IPv4        bool // an IPv4 STUN round trip completed
	IPv6CanSend bool // an IPv6 packet was able to be sent
	IPv4CanSend bool // an IPv4 packet was able to be sent
	OSHasIPv6   bool // could bind a socket to ::1

	// MappingVariesByDestIP is whether STUN results depend which
	// STUN server you're talking to (on IPv4).
	MappingVariesByDestIP opt.Bool

	// HairPinning is whether the router supports communicating
	// between two local devices through the NATted public IP address
	// (on IPv4).
	HairPinning opt.Bool

	// UPnP is whether UPnP appears present on the LAN.
	// Empty means not checked.
	UPnP opt.Bool
	// PMP is whether NAT-PMP appears present on the LAN.
	// Empty means not checked.
	PMP opt.Bool
	// PCP is whether PCP appears present on the LAN.
	// Empty means not checked.
	PCP opt.Bool

	PreferredDERP   int                   // or 0 for unknown
	RegionLatency   map[int]time.Duration // keyed by DERP Region ID
	RegionV4Latency map[int]time.Duration // keyed by DERP Region ID
	RegionV6Latency map[int]time.Duration // keyed by DERP Region ID

	GlobalV4 string // ip:port of global IPv4
	GlobalV6 string // [ip]:port of global IPv6

	// TODO: update Clone when adding new fields
}

// AnyPortMappingChecked reports whether any of UPnP, PMP, or PCP are non-empty.
func (r *Report) AnyPortMappingChecked() bool {
	return r.UPnP != "" || r.PMP != "" || r.PCP != ""
}

func (r *Report) Clone() *Report {
	if r == nil {
		return nil
	}
	r2 := *r
	r2.RegionLatency = cloneDurationMap(r2.RegionLatency)
	r2.RegionV4Latency = cloneDurationMap(r2.RegionV4Latency)
	r2.RegionV6Latency = cloneDurationMap(r2.RegionV6Latency)
	return &r2
}

func cloneDurationMap(m map[int]time.Duration) map[int]time.Duration {
	if m == nil {
		return nil
	}
	m2 := make(map[int]time.Duration, len(m))
	for k, v := range m {
		m2[k] = v
	}
	return m2
}

// Client generates a netcheck Report.
type Client struct {
	// Verbose enables verbose logging.
	Verbose bool

	// Logf optionally specifies where to log to.
	// If nil, log.Printf is used.
	Logf logger.Logf

	// TimeNow, if non-nil, is used instead of time.Now.
	TimeNow func() time.Time

	// GetSTUNConn4 optionally provides a func to return the
	// connection to use for sending & receiving IPv4 packets. If
	// nil, an emphemeral one is created as needed.
	GetSTUNConn4 func() STUNConn

	// GetSTUNConn6 is like GetSTUNConn4, but for IPv6.
	GetSTUNConn6 func() STUNConn

	// SkipExternalNetwork controls whether the client should not try
	// to reach things other than localhost. This is set to true
	// in tests to avoid probing the local LAN's router, etc.
	SkipExternalNetwork bool

	// UDPBindAddr, if non-empty, is the address to listen on for UDP.
	// It defaults to ":0".
	UDPBindAddr string

	// PortMapper, if non-nil, is used for portmap queries.
	// If nil, portmap discovery is not done.
	PortMapper *portmapper.Client // lazily initialized on first use

	mu       sync.Mutex            // guards following
	nextFull bool                  // do a full region scan, even if last != nil
	prev     map[time.Time]*Report // some previous reports
	last     *Report               // most recent report
	lastFull time.Time             // time of last full (non-incremental) report
	curState *reportState          // non-nil if we're in a call to GetReportn
}

// STUNConn is the interface required by the netcheck Client when
// reusing an existing UDP connection.
type STUNConn interface {
	WriteToUDPAddrPort([]byte, netip.AddrPort) (int, error)
	WriteTo([]byte, net.Addr) (int, error)
	ReadFrom([]byte) (int, net.Addr, error)
}

func (c *Client) enoughRegions() int {
	if c.Verbose {
		// Abuse verbose a bit here so netcheck can show all region latencies
		// in verbose mode.
		return 100
	}
	return 3
}

func (c *Client) logf(format string, a ...any) {
	if c.Logf != nil {
		c.Logf(format, a...)
	} else {
		log.Printf(format, a...)
	}
}

func (c *Client) vlogf(format string, a ...any) {
	if c.Verbose || debugNetcheck {
		c.logf(format, a...)
	}
}

// handleHairSTUN reports whether pkt (from src) was our magic hairpin
// probe packet that we sent to ourselves.
func (c *Client) handleHairSTUNLocked(pkt []byte, src netip.AddrPort) bool {
	rs := c.curState
	if rs == nil {
		return false
	}
	if tx, err := stun.ParseBindingRequest(pkt); err == nil && tx == rs.hairTX {
		select {
		case rs.gotHairSTUN <- src:
		default:
		}
		return true
	}
	return false
}

// MakeNextReportFull forces the next GetReport call to be a full
// (non-incremental) probe of all DERP regions.
func (c *Client) MakeNextReportFull() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextFull = true
}

func (c *Client) ReceiveSTUNPacket(pkt []byte, src netip.AddrPort) {
	c.vlogf("received STUN packet from %s", src)

	if src.Addr().Is4() {
		metricSTUNRecv4.Add(1)
	} else if src.Addr().Is6() {
		metricSTUNRecv6.Add(1)
	}

	c.mu.Lock()
	if c.handleHairSTUNLocked(pkt, src) {
		c.mu.Unlock()
		return
	}
	rs := c.curState
	c.mu.Unlock()

	if rs == nil {
		return
	}

	tx, addr, port, err := stun.ParseResponse(pkt)
	if err != nil {
		if _, err := stun.ParseBindingRequest(pkt); err == nil {
			// This was probably our own netcheck hairpin
			// check probe coming in late. Ignore.
			return
		}
		c.logf("netcheck: received unexpected STUN message response from %v: %v", src, err)
		return
	}

	rs.mu.Lock()
	onDone, ok := rs.inFlight[tx]
	if ok {
		delete(rs.inFlight, tx)
	}
	rs.mu.Unlock()
	if ok {
		if ipp, ok := netaddr.FromStdAddr(addr, int(port), ""); ok {
			onDone(ipp)
		}
	}
}

// probeProto is the protocol used to time a node's latency.
type probeProto uint8

const (
	probeIPv4  probeProto = iota // STUN IPv4
	probeIPv6                    // STUN IPv6
	probeHTTPS                   // HTTPS
)

type probe struct {
	// delay is when the probe is started, relative to the time
	// that GetReport is called. One probe in each probePlan
	// should have a delay of 0. Non-zero values are for retries
	// on UDP loss or timeout.
	delay time.Duration

	// node is the name of the node name. DERP node names are globally
	// unique so there's no region ID.
	node string

	// proto is how the node should be probed.
	proto probeProto

	// wait is how long to wait until the probe is considered failed.
	// 0 means to use a default value.
	wait time.Duration
}

// probePlan is a set of node probes to run.
// The map key is a descriptive name, only used for tests.
//
// The values are logically an unordered set of tests to run concurrently.
// In practice there's some order to them based on their delay fields,
// but multiple probes can have the same delay time or be running concurrently
// both within and between sets.
//
// A set of probes is done once either one of the probes completes, or
// the next probe to run wouldn't yield any new information not
// already discovered by any previous probe in any set.
type probePlan map[string][]probe

// sortRegions returns the regions of dm first sorted
// from fastest to slowest (based on the 'last' report),
// end in regions that have no data.
func sortRegions(dm *tailcfg.DERPMap, last *Report) (prev []*tailcfg.DERPRegion) {
	prev = make([]*tailcfg.DERPRegion, 0, len(dm.Regions))
	for _, reg := range dm.Regions {
		if reg.Avoid {
			continue
		}
		prev = append(prev, reg)
	}
	sort.Slice(prev, func(i, j int) bool {
		da, db := last.RegionLatency[prev[i].RegionID], last.RegionLatency[prev[j].RegionID]
		if db == 0 && da != 0 {
			// Non-zero sorts before zero.
			return true
		}
		if da == 0 {
			// Zero can't sort before anything else.
			return false
		}
		return da < db
	})
	return prev
}

// numIncrementalRegions is the number of fastest regions to
// periodically re-query during incremental netcheck reports. (During
// a full report, all regions are scanned.)
const numIncrementalRegions = 3

// makeProbePlan generates the probe plan for a DERPMap, given the most
// recent report and whether IPv6 is configured on an interface.
func makeProbePlan(dm *tailcfg.DERPMap, ifState *interfaces.State, last *Report) (plan probePlan) {
	if last == nil || len(last.RegionLatency) == 0 {
		return makeProbePlanInitial(dm, ifState)
	}
	have6if := ifState.HaveV6
	have4if := ifState.HaveV4
	plan = make(probePlan)
	if !have4if && !have6if {
		return plan
	}
	had4 := len(last.RegionV4Latency) > 0
	had6 := len(last.RegionV6Latency) > 0
	hadBoth := have6if && had4 && had6
	for ri, reg := range sortRegions(dm, last) {
		if ri == numIncrementalRegions {
			break
		}
		var p4, p6 []probe
		do4 := have4if
		do6 := have6if

		// By default, each node only gets one STUN packet sent,
		// except the fastest two from the previous round.
		tries := 1
		isFastestTwo := ri < 2

		if isFastestTwo {
			tries = 2
		} else if hadBoth {
			// For dual stack machines, make the 3rd & slower nodes alternate
			// between.
			if ri%2 == 0 {
				do4, do6 = true, false
			} else {
				do4, do6 = false, true
			}
		}
		if !isFastestTwo && !had6 {
			do6 = false
		}

		if reg.RegionID == last.PreferredDERP {
			// But if we already had a DERP home, try extra hard to
			// make sure it's there so we don't flip flop around.
			tries = 4
		}

		for try := 0; try < tries; try++ {
			if len(reg.Nodes) == 0 {
				// Shouldn't be possible.
				continue
			}
			if try != 0 && !had6 {
				do6 = false
			}
			n := reg.Nodes[try%len(reg.Nodes)]
			prevLatency := last.RegionLatency[reg.RegionID] * 120 / 100
			if prevLatency == 0 {
				prevLatency = defaultActiveRetransmitTime
			}
			delay := time.Duration(try) * prevLatency
			if try > 1 {
				delay += time.Duration(try) * 50 * time.Millisecond
			}
			if do4 {
				p4 = append(p4, probe{delay: delay, node: n.Name, proto: probeIPv4})
			}
			if do6 {
				p6 = append(p6, probe{delay: delay, node: n.Name, proto: probeIPv6})
			}
		}
		if len(p4) > 0 {
			plan[fmt.Sprintf("region-%d-v4", reg.RegionID)] = p4
		}
		if len(p6) > 0 {
			plan[fmt.Sprintf("region-%d-v6", reg.RegionID)] = p6
		}
	}
	return plan
}

func makeProbePlanInitial(dm *tailcfg.DERPMap, ifState *interfaces.State) (plan probePlan) {
	plan = make(probePlan)

	for _, reg := range dm.Regions {
		var p4 []probe
		var p6 []probe
		for try := 0; try < 3; try++ {
			n := reg.Nodes[try%len(reg.Nodes)]
			delay := time.Duration(try) * defaultInitialRetransmitTime
			if ifState.HaveV4 && nodeMight4(n) {
				p4 = append(p4, probe{delay: delay, node: n.Name, proto: probeIPv4})
			}
			if ifState.HaveV6 && nodeMight6(n) {
				p6 = append(p6, probe{delay: delay, node: n.Name, proto: probeIPv6})
			}
		}
		if len(p4) > 0 {
			plan[fmt.Sprintf("region-%d-v4", reg.RegionID)] = p4
		}
		if len(p6) > 0 {
			plan[fmt.Sprintf("region-%d-v6", reg.RegionID)] = p6
		}
	}
	return plan
}

// nodeMight6 reports whether n might reply to STUN over IPv6 based on
// its config alone, without DNS lookups. It only returns false if
// it's not explicitly disabled.
func nodeMight6(n *tailcfg.DERPNode) bool {
	if n.IPv6 == "" {
		return true
	}
	ip, _ := netip.ParseAddr(n.IPv6)
	return ip.Is6()

}

// nodeMight4 reports whether n might reply to STUN over IPv4 based on
// its config alone, without DNS lookups. It only returns false if
// it's not explicitly disabled.
func nodeMight4(n *tailcfg.DERPNode) bool {
	if n.IPv4 == "" {
		return true
	}
	ip, _ := netip.ParseAddr(n.IPv4)
	return ip.Is4()
}

// readPackets reads STUN packets from pc until there's an error or ctx is done.
// In either case, it closes pc.
func (c *Client) readPackets(ctx context.Context, pc net.PacketConn) {
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		pc.Close()
	}()

	var buf [64 << 10]byte
	for {
		n, addr, err := pc.ReadFrom(buf[:])
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logf("ReadFrom: %v", err)
			return
		}
		ua, ok := addr.(*net.UDPAddr)
		if !ok {
			c.logf("ReadFrom: unexpected addr %T", addr)
			continue
		}
		pkt := buf[:n]
		if !stun.Is(pkt) {
			continue
		}
		if ipp, ok := netaddr.FromStdAddr(ua.IP, ua.Port, ua.Zone); ok {
			c.ReceiveSTUNPacket(pkt, ipp)
		}
	}
}

// reportState holds the state for a single invocation of Client.GetReport.
type reportState struct {
	c           *Client
	hairTX      stun.TxID
	gotHairSTUN chan netip.AddrPort
	hairTimeout chan struct{} // closed on timeout
	pc4         STUNConn
	pc6         STUNConn
	pc4Hair     nettype.PacketConn
	incremental bool // doing a lite, follow-up netcheck
	stopProbeCh chan struct{}
	waitPortMap sync.WaitGroup

	mu            sync.Mutex
	sentHairCheck bool
	report        *Report                            // to be returned by GetReport
	inFlight      map[stun.TxID]func(netip.AddrPort) // called without c.mu held
	gotEP4        string
	timers        []*time.Timer
}

func (rs *reportState) anyUDP() bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.report.UDP
}

func (rs *reportState) haveRegionLatency(regionID int) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	_, ok := rs.report.RegionLatency[regionID]
	return ok
}

// probeWouldHelp reports whether executing the given probe would
// yield any new information.
// The given node is provided just because the sole caller already has it
// and it saves a lookup.
func (rs *reportState) probeWouldHelp(probe probe, node *tailcfg.DERPNode) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	// If the probe is for a region we don't yet know about, that
	// would help.
	if _, ok := rs.report.RegionLatency[node.RegionID]; !ok {
		return true
	}

	// If the probe is for IPv6 and we don't yet have an IPv6
	// report, that would help.
	if probe.proto == probeIPv6 && len(rs.report.RegionV6Latency) == 0 {
		return true
	}

	// For IPv4, we need at least two IPv4 results overall to
	// determine whether we're behind a NAT that shows us as
	// different source IPs and/or ports depending on who we're
	// talking to. If we don't yet have two results yet
	// (MappingVariesByDestIP is blank), then another IPv4 probe
	// would be good.
	if probe.proto == probeIPv4 && rs.report.MappingVariesByDestIP == "" {
		return true
	}

	// Otherwise not interesting.
	return false
}

func (rs *reportState) startHairCheckLocked(dst netip.AddrPort) {
	if rs.sentHairCheck || rs.incremental {
		return
	}
	rs.sentHairCheck = true
	rs.pc4Hair.WriteToUDPAddrPort(stun.Request(rs.hairTX), dst)
	rs.c.vlogf("sent haircheck to %v", dst)
	time.AfterFunc(hairpinCheckTimeout, func() { close(rs.hairTimeout) })
}

func (rs *reportState) waitHairCheck(ctx context.Context) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	ret := rs.report
	if rs.incremental {
		if rs.c.last != nil {
			ret.HairPinning = rs.c.last.HairPinning
		}
		return
	}
	if !rs.sentHairCheck {
		return
	}

	select {
	case <-rs.gotHairSTUN:
		ret.HairPinning.Set(true)
	case <-rs.hairTimeout:
		rs.c.vlogf("hairCheck timeout")
		ret.HairPinning.Set(false)
	default:
		select {
		case <-rs.gotHairSTUN:
			ret.HairPinning.Set(true)
		case <-rs.hairTimeout:
			ret.HairPinning.Set(false)
		case <-ctx.Done():
		}
	}
}

func (rs *reportState) stopTimers() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, t := range rs.timers {
		t.Stop()
	}
}

// addNodeLatency updates rs to note that node's latency is d. If ipp
// is non-zero (for all but HTTPS replies), it's recorded as our UDP
// IP:port.
func (rs *reportState) addNodeLatency(node *tailcfg.DERPNode, ipp netip.AddrPort, d time.Duration) {
	var ipPortStr string
	if ipp != (netip.AddrPort{}) {
		ipPortStr = net.JoinHostPort(ipp.Addr().String(), fmt.Sprint(ipp.Port()))
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()
	ret := rs.report

	ret.UDP = true
	updateLatency(ret.RegionLatency, node.RegionID, d)

	// Once we've heard from enough regions (3), start a timer to
	// give up on the other ones. The timer's duration is a
	// function of whether this is our initial full probe or an
	// incremental one. For incremental ones, wait for the
	// duration of the slowest region. For initial ones, double
	// that.
	if len(ret.RegionLatency) == rs.c.enoughRegions() {
		timeout := maxDurationValue(ret.RegionLatency)
		if !rs.incremental {
			timeout *= 2
		}
		rs.timers = append(rs.timers, time.AfterFunc(timeout, rs.stopProbes))
	}

	switch {
	case ipp.Addr().Is6():
		updateLatency(ret.RegionV6Latency, node.RegionID, d)
		ret.IPv6 = true
		ret.GlobalV6 = ipPortStr
		// TODO: track MappingVariesByDestIP for IPv6
		// too? Would be sad if so, but who knows.
	case ipp.Addr().Is4():
		updateLatency(ret.RegionV4Latency, node.RegionID, d)
		ret.IPv4 = true
		if rs.gotEP4 == "" {
			rs.gotEP4 = ipPortStr
			ret.GlobalV4 = ipPortStr
			rs.startHairCheckLocked(ipp)
		} else {
			if rs.gotEP4 != ipPortStr {
				ret.MappingVariesByDestIP.Set(true)
			} else if ret.MappingVariesByDestIP == "" {
				ret.MappingVariesByDestIP.Set(false)
			}
		}
	}
}

func (rs *reportState) stopProbes() {
	select {
	case rs.stopProbeCh <- struct{}{}:
	default:
	}
}

func (rs *reportState) setOptBool(b *opt.Bool, v bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	b.Set(v)
}

func (rs *reportState) probePortMapServices() {
	defer rs.waitPortMap.Done()

	rs.setOptBool(&rs.report.UPnP, false)
	rs.setOptBool(&rs.report.PMP, false)
	rs.setOptBool(&rs.report.PCP, false)

	res, err := rs.c.PortMapper.Probe(context.Background())
	if err != nil {
		if !errors.Is(err, portmapper.ErrGatewayRange) {
			// "skipping portmap; gateway range likely lacks support"
			// is not very useful, and too spammy on cloud systems.
			// If there are other errors, we want to log those.
			rs.c.logf("probePortMapServices: %v", err)
		}
		return
	}

	rs.setOptBool(&rs.report.UPnP, res.UPnP)
	rs.setOptBool(&rs.report.PMP, res.PMP)
	rs.setOptBool(&rs.report.PCP, res.PCP)
}

func newReport() *Report {
	return &Report{
		RegionLatency:   make(map[int]time.Duration),
		RegionV4Latency: make(map[int]time.Duration),
		RegionV6Latency: make(map[int]time.Duration),
	}
}

func (c *Client) udpBindAddr() string {
	if v := c.UDPBindAddr; v != "" {
		return v
	}
	return ":0"
}

// GetReport gets a report.
//
// It may not be called concurrently with itself.
func (c *Client) GetReport(ctx context.Context, dm *tailcfg.DERPMap) (_ *Report, reterr error) {
	defer func() {
		if reterr != nil {
			metricNumGetReportError.Add(1)
		}
	}()
	metricNumGetReport.Add(1)
	// Mask user context with ours that we guarantee to cancel so
	// we can depend on it being closed in goroutines later.
	// (User ctx might be context.Background, etc)
	ctx, cancel := context.WithTimeout(ctx, overallProbeTimeout)
	defer cancel()

	if dm == nil {
		return nil, errors.New("netcheck: GetReport: DERP map is nil")
	}

	c.mu.Lock()
	if c.curState != nil {
		c.mu.Unlock()
		return nil, errors.New("invalid concurrent call to GetReport")
	}
	rs := &reportState{
		c:           c,
		report:      newReport(),
		inFlight:    map[stun.TxID]func(netip.AddrPort){},
		hairTX:      stun.NewTxID(), // random payload
		gotHairSTUN: make(chan netip.AddrPort, 1),
		hairTimeout: make(chan struct{}),
		stopProbeCh: make(chan struct{}, 1),
	}
	c.curState = rs
	last := c.last
	now := c.timeNow()
	if c.nextFull || now.Sub(c.lastFull) > 5*time.Minute {
		last = nil // causes makeProbePlan below to do a full (initial) plan
		c.nextFull = false
		c.lastFull = now
		metricNumGetReportFull.Add(1)
	}
	rs.incremental = last != nil
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.curState = nil
	}()

	if runtime.GOOS == "js" {
		if err := c.runHTTPOnlyChecks(ctx, last, rs, dm); err != nil {
			return nil, err
		}
		return c.finishAndStoreReport(rs, dm), nil
	}

	ifState, err := interfaces.GetState()
	if err != nil {
		c.logf("[v1] interfaces: %v", err)
		return nil, err
	}

	// See if IPv6 works at all, or if it's been hard disabled at the
	// OS level.
	v6udp, err := nettype.MakePacketListenerWithNetIP(netns.Listener(c.logf)).ListenPacket(ctx, "udp6", "[::1]:0")
	if err == nil {
		rs.report.OSHasIPv6 = true
		v6udp.Close()
	}

	// Create a UDP4 socket used for sending to our discovered IPv4 address.
	rs.pc4Hair, err = nettype.MakePacketListenerWithNetIP(netns.Listener(c.logf)).ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		c.logf("udp4: %v", err)
		return nil, err
	}
	defer rs.pc4Hair.Close()

	if !c.SkipExternalNetwork && c.PortMapper != nil {
		rs.waitPortMap.Add(1)
		go rs.probePortMapServices()
	}

	// At least the Apple Airport Extreme doesn't allow hairpin
	// sends from a private socket until it's seen traffic from
	// that src IP:port to something else out on the internet.
	//
	// See https://github.com/tailscale/tailscale/issues/188#issuecomment-600728643
	//
	// And it seems that even sending to a likely-filtered RFC 5737
	// documentation-only IPv4 range is enough to set up the mapping.
	// So do that for now. In the future we might want to classify networks
	// that do and don't require this separately. But for now help it.
	const documentationIP = "203.0.113.1"
	rs.pc4Hair.WriteTo([]byte("tailscale netcheck; see https://github.com/tailscale/tailscale/issues/188"), &net.UDPAddr{IP: net.ParseIP(documentationIP), Port: 12345})

	if f := c.GetSTUNConn4; f != nil {
		rs.pc4 = f()
	} else {
		u4, err := nettype.MakePacketListenerWithNetIP(netns.Listener(c.logf)).ListenPacket(ctx, "udp4", c.udpBindAddr())
		if err != nil {
			c.logf("udp4: %v", err)
			return nil, err
		}
		rs.pc4 = u4
		go c.readPackets(ctx, u4)
	}

	if ifState.HaveV6 {
		if f := c.GetSTUNConn6; f != nil {
			rs.pc6 = f()
		} else {
			u6, err := nettype.MakePacketListenerWithNetIP(netns.Listener(c.logf)).ListenPacket(ctx, "udp6", c.udpBindAddr())
			if err != nil {
				c.logf("udp6: %v", err)
			} else {
				rs.pc6 = u6
				go c.readPackets(ctx, u6)
			}
		}
	}

	plan := makeProbePlan(dm, ifState, last)

	wg := syncs.NewWaitGroupChan()
	wg.Add(len(plan))
	for _, probeSet := range plan {
		setCtx, cancelSet := context.WithCancel(ctx)
		go func(probeSet []probe) {
			pwg := syncs.NewWaitGroupChan()
			pwg.Add(len(probeSet))
			for _, p := range probeSet {
				go func(probe probe) {
					rs.runProbe(setCtx, dm, probe, cancelSet)
					pwg.Decr()
				}(p)
			}
			select {
			case <-setCtx.Done():
			case <-pwg.DoneChan():
			}
			wg.Decr()
		}(probeSet)
	}

	stunTimer := time.NewTimer(stunProbeTimeout)
	defer stunTimer.Stop()

	select {
	case <-stunTimer.C:
	case <-ctx.Done():
	case <-wg.DoneChan():
	case <-rs.stopProbeCh:
		// Saw enough regions.
		c.vlogf("saw enough regions; not waiting for rest")
	}

	rs.waitHairCheck(ctx)
	c.vlogf("hairCheck done")
	if !c.SkipExternalNetwork && c.PortMapper != nil {
		rs.waitPortMap.Wait()
		c.vlogf("portMap done")
	}
	rs.stopTimers()

	// Try HTTPS latency check if all STUN probes failed due to UDP presumably being blocked.
	// TODO: this should be moved into the probePlan, using probeProto probeHTTPS.
	if !rs.anyUDP() && ctx.Err() == nil {
		var wg sync.WaitGroup
		var need []*tailcfg.DERPRegion
		for rid, reg := range dm.Regions {
			if !rs.haveRegionLatency(rid) && regionHasDERPNode(reg) {
				need = append(need, reg)
			}
		}
		if len(need) > 0 {
			wg.Add(len(need))
			c.logf("netcheck: UDP is blocked, trying HTTPS")
		}
		for _, reg := range need {
			go func(reg *tailcfg.DERPRegion) {
				defer wg.Done()
				if d, ip, err := c.measureHTTPSLatency(ctx, reg); err != nil {
					c.logf("[v1] netcheck: measuring HTTPS latency of %v (%d): %v", reg.RegionCode, reg.RegionID, err)
				} else {
					rs.mu.Lock()
					rs.report.RegionLatency[reg.RegionID] = d
					// We set these IPv4 and IPv6 but they're not really used
					// and we don't necessarily set them both. If UDP is blocked
					// and both IPv4 and IPv6 are available over TCP, it's basically
					// random which fields end up getting set here.
					// Since they're not needed, that's fine for now.
					if ip.Is4() {
						rs.report.IPv4 = true
					}
					if ip.Is6() {
						rs.report.IPv6 = true
					}
					rs.mu.Unlock()
				}
			}(reg)
		}
		wg.Wait()
	}

	return c.finishAndStoreReport(rs, dm), nil
}

func (c *Client) finishAndStoreReport(rs *reportState, dm *tailcfg.DERPMap) *Report {
	rs.mu.Lock()
	report := rs.report.Clone()
	rs.mu.Unlock()

	c.addReportHistoryAndSetPreferredDERP(report)
	c.logConciseReport(report, dm)

	return report
}

// runHTTPOnlyChecks is the netcheck done by environments that can
// only do HTTP requests, such as ws/wasm.
func (c *Client) runHTTPOnlyChecks(ctx context.Context, last *Report, rs *reportState, dm *tailcfg.DERPMap) error {
	var regions []*tailcfg.DERPRegion
	if rs.incremental && last != nil {
		for rid := range last.RegionLatency {
			if dr, ok := dm.Regions[rid]; ok {
				regions = append(regions, dr)
			}
		}
	}
	if len(regions) == 0 {
		for _, dr := range dm.Regions {
			regions = append(regions, dr)
		}
	}
	c.logf("running HTTP-only netcheck against %v regions", len(regions))

	var wg sync.WaitGroup
	for _, rg := range regions {
		if len(rg.Nodes) == 0 {
			continue
		}
		wg.Add(1)
		rg := rg
		go func() {
			defer wg.Done()
			node := rg.Nodes[0]
			req, _ := http.NewRequestWithContext(ctx, "HEAD", "https://"+node.HostName+"/derp/probe", nil)
			// One warm-up one to get HTTP connection set
			// up and get a connection from the browser's
			// pool.
			if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode > 299 {
				if err != nil {
					c.logf("probing %s: %v", node.HostName, err)
				} else {
					c.logf("probing %s: unexpected status %s", node.HostName, r.Status)
				}
				return
			}
			t0 := c.timeNow()
			if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode > 299 {
				if err != nil {
					c.logf("probing %s: %v", node.HostName, err)
				} else {
					c.logf("probing %s: unexpected status %s", node.HostName, r.Status)
				}
				return
			}
			d := c.timeNow().Sub(t0)
			rs.addNodeLatency(node, netip.AddrPort{}, d)
		}()
	}
	wg.Wait()
	return nil
}

func (c *Client) measureHTTPSLatency(ctx context.Context, reg *tailcfg.DERPRegion) (time.Duration, netip.Addr, error) {
	metricHTTPSend.Add(1)
	var result httpstat.Result
	ctx, cancel := context.WithTimeout(httpstat.WithHTTPStat(ctx, &result), overallProbeTimeout)
	defer cancel()

	var ip netip.Addr

	dc := derphttp.NewNetcheckClient(c.logf)
	tlsConn, tcpConn, node, err := dc.DialRegionTLS(ctx, reg)
	if err != nil {
		return 0, ip, err
	}
	defer tcpConn.Close()

	if ta, ok := tlsConn.RemoteAddr().(*net.TCPAddr); ok {
		ip, _ = netaddr.FromStdIP(ta.IP)
	}
	if ip == (netip.Addr{}) {
		return 0, ip, fmt.Errorf("no unexpected RemoteAddr %#v", tlsConn.RemoteAddr())
	}

	connc := make(chan *tls.Conn, 1)
	connc <- tlsConn

	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, errors.New("unexpected DialContext dial")
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			select {
			case nc := <-connc:
				return nc, nil
			default:
				return nil, errors.New("only one conn expected")
			}
		},
	}
	hc := &http.Client{Transport: tr}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://"+node.HostName+"/derp/latency-check", nil)
	if err != nil {
		return 0, ip, err
	}

	resp, err := hc.Do(req)
	if err != nil {
		return 0, ip, err
	}
	defer resp.Body.Close()

	// DERPs should give us a nominal status code, so anything else is probably
	// an access denied by a MITM proxy (or at the very least a signal not to
	// trust this latency check).
	if resp.StatusCode > 299 {
		return 0, ip, fmt.Errorf("unexpected status code: %d (%s)", resp.StatusCode, resp.Status)
	}

	_, err = io.Copy(ioutil.Discard, io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return 0, ip, err
	}
	result.End(c.timeNow())

	// TODO: decide best timing heuristic here.
	// Maybe the server should return the tcpinfo_rtt?
	return result.ServerProcessing, ip, nil
}

func (c *Client) logConciseReport(r *Report, dm *tailcfg.DERPMap) {
	c.logf("[v1] report: %v", logger.ArgWriter(func(w *bufio.Writer) {
		fmt.Fprintf(w, "udp=%v", r.UDP)
		if !r.IPv4 {
			fmt.Fprintf(w, " v4=%v", r.IPv4)
		}

		fmt.Fprintf(w, " v6=%v", r.IPv6)
		fmt.Fprintf(w, " mapvarydest=%v", r.MappingVariesByDestIP)
		fmt.Fprintf(w, " hair=%v", r.HairPinning)
		if r.AnyPortMappingChecked() {
			fmt.Fprintf(w, " portmap=%v%v%v", conciseOptBool(r.UPnP, "U"), conciseOptBool(r.PMP, "M"), conciseOptBool(r.PCP, "C"))
		} else {
			fmt.Fprintf(w, " portmap=?")
		}
		if r.GlobalV4 != "" {
			fmt.Fprintf(w, " v4a=%v", r.GlobalV4)
		}
		if r.GlobalV6 != "" {
			fmt.Fprintf(w, " v6a=%v", r.GlobalV6)
		}
		fmt.Fprintf(w, " derp=%v", r.PreferredDERP)
		if r.PreferredDERP != 0 {
			fmt.Fprintf(w, " derpdist=")
			needComma := false
			for _, rid := range dm.RegionIDs() {
				if d := r.RegionV4Latency[rid]; d != 0 {
					if needComma {
						w.WriteByte(',')
					}
					fmt.Fprintf(w, "%dv4:%v", rid, d.Round(time.Millisecond))
					needComma = true
				}
				if d := r.RegionV6Latency[rid]; d != 0 {
					if needComma {
						w.WriteByte(',')
					}
					fmt.Fprintf(w, "%dv6:%v", rid, d.Round(time.Millisecond))
					needComma = true
				}
			}
		}
	}))
}

func (c *Client) timeNow() time.Time {
	if c.TimeNow != nil {
		return c.TimeNow()
	}
	return time.Now()
}

// addReportHistoryAndSetPreferredDERP adds r to the set of recent Reports
// and mutates r.PreferredDERP to contain the best recent one.
func (c *Client) addReportHistoryAndSetPreferredDERP(r *Report) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var prevDERP int
	if c.last != nil {
		prevDERP = c.last.PreferredDERP
	}
	if c.prev == nil {
		c.prev = map[time.Time]*Report{}
	}
	now := c.timeNow()
	c.prev[now] = r
	c.last = r

	const maxAge = 5 * time.Minute

	// region ID => its best recent latency in last maxAge
	bestRecent := map[int]time.Duration{}

	for t, pr := range c.prev {
		if now.Sub(t) > maxAge {
			delete(c.prev, t)
			continue
		}
		for regionID, d := range pr.RegionLatency {
			if bd, ok := bestRecent[regionID]; !ok || d < bd {
				bestRecent[regionID] = d
			}
		}
	}

	// Then, pick which currently-alive DERP server from the
	// current report has the best latency over the past maxAge.
	var bestAny time.Duration
	var oldRegionCurLatency time.Duration
	for regionID, d := range r.RegionLatency {
		if regionID == prevDERP {
			oldRegionCurLatency = d
		}
		best := bestRecent[regionID]
		if r.PreferredDERP == 0 || best < bestAny {
			bestAny = best
			r.PreferredDERP = regionID
		}
	}

	// If we're changing our preferred DERP but the old one's still
	// accessible and the new one's not much better, just stick with
	// where we are.
	if prevDERP != 0 &&
		r.PreferredDERP != prevDERP &&
		oldRegionCurLatency != 0 &&
		bestAny > oldRegionCurLatency/3*2 {
		r.PreferredDERP = prevDERP
	}
}

func updateLatency(m map[int]time.Duration, regionID int, d time.Duration) {
	if prev, ok := m[regionID]; !ok || d < prev {
		m[regionID] = d
	}
}

func namedNode(dm *tailcfg.DERPMap, nodeName string) *tailcfg.DERPNode {
	if dm == nil {
		return nil
	}
	for _, r := range dm.Regions {
		for _, n := range r.Nodes {
			if n.Name == nodeName {
				return n
			}
		}
	}
	return nil
}

func (rs *reportState) runProbe(ctx context.Context, dm *tailcfg.DERPMap, probe probe, cancelSet func()) {
	c := rs.c
	node := namedNode(dm, probe.node)
	if node == nil {
		c.logf("netcheck.runProbe: named node %q not found", probe.node)
		return
	}

	if probe.delay > 0 {
		delayTimer := time.NewTimer(probe.delay)
		select {
		case <-delayTimer.C:
		case <-ctx.Done():
			delayTimer.Stop()
			return
		}
	}

	if !rs.probeWouldHelp(probe, node) {
		cancelSet()
		return
	}

	addr := c.nodeAddr(ctx, node, probe.proto)
	if !addr.IsValid() {
		return
	}

	txID := stun.NewTxID()
	req := stun.Request(txID)

	sent := time.Now() // after DNS lookup above

	rs.mu.Lock()
	rs.inFlight[txID] = func(ipp netip.AddrPort) {
		rs.addNodeLatency(node, ipp, time.Since(sent))
		cancelSet() // abort other nodes in this set
	}
	rs.mu.Unlock()

	switch probe.proto {
	case probeIPv4:
		metricSTUNSend4.Add(1)
		n, err := rs.pc4.WriteToUDPAddrPort(req, addr)
		if n == len(req) && err == nil || neterror.TreatAsLostUDP(err) {
			rs.mu.Lock()
			rs.report.IPv4CanSend = true
			rs.mu.Unlock()
		}
	case probeIPv6:
		metricSTUNSend6.Add(1)
		n, err := rs.pc6.WriteToUDPAddrPort(req, addr)
		if n == len(req) && err == nil || neterror.TreatAsLostUDP(err) {
			rs.mu.Lock()
			rs.report.IPv6CanSend = true
			rs.mu.Unlock()
		}
	default:
		panic("bad probe proto " + fmt.Sprint(probe.proto))
	}
	c.vlogf("sent to %v", addr)
}

// proto is 4 or 6
// If it returns nil, the node is skipped.
func (c *Client) nodeAddr(ctx context.Context, n *tailcfg.DERPNode, proto probeProto) (ap netip.AddrPort) {
	port := n.STUNPort
	if port == 0 {
		port = 3478
	}
	if port < 0 || port > 1<<16-1 {
		return
	}
	if n.STUNTestIP != "" {
		ip, err := netip.ParseAddr(n.STUNTestIP)
		if err != nil {
			return
		}
		if proto == probeIPv4 && ip.Is6() {
			return
		}
		if proto == probeIPv6 && ip.Is4() {
			return
		}
		return netip.AddrPortFrom(ip, uint16(port))
	}

	switch proto {
	case probeIPv4:
		if n.IPv4 != "" {
			ip, _ := netip.ParseAddr(n.IPv4)
			if !ip.Is4() {
				return
			}
			return netip.AddrPortFrom(ip, uint16(port))
		}
	case probeIPv6:
		if n.IPv6 != "" {
			ip, _ := netip.ParseAddr(n.IPv6)
			if !ip.Is6() {
				return
			}
			return netip.AddrPortFrom(ip, uint16(port))
		}
	default:
		return
	}

	// TODO(bradfitz): add singleflight+dnscache here.
	addrs, _ := net.DefaultResolver.LookupIPAddr(ctx, n.HostName)
	for _, a := range addrs {
		if (a.IP.To4() != nil) == (proto == probeIPv4) {
			na, _ := netaddr.FromStdIP(a.IP.To4())
			return netip.AddrPortFrom(na, uint16(port))
		}
	}
	return
}

func regionHasDERPNode(r *tailcfg.DERPRegion) bool {
	for _, n := range r.Nodes {
		if !n.STUNOnly {
			return true
		}
	}
	return false
}

func maxDurationValue(m map[int]time.Duration) (max time.Duration) {
	for _, v := range m {
		if v > max {
			max = v
		}
	}
	return max
}

func conciseOptBool(b opt.Bool, trueVal string) string {
	if b == "" {
		return "_"
	}
	v, ok := b.Get()
	if !ok {
		return "x"
	}
	if v {
		return trueVal
	}
	return ""
}

var (
	metricNumGetReport      = clientmetric.NewCounter("netcheck_report")
	metricNumGetReportFull  = clientmetric.NewCounter("netcheck_report_full")
	metricNumGetReportError = clientmetric.NewCounter("netcheck_report_error")

	metricSTUNSend4 = clientmetric.NewCounter("netcheck_stun_send_ipv4")
	metricSTUNSend6 = clientmetric.NewCounter("netcheck_stun_send_ipv6")
	metricSTUNRecv4 = clientmetric.NewCounter("netcheck_stun_recv_ipv4")
	metricSTUNRecv6 = clientmetric.NewCounter("netcheck_stun_recv_ipv6")
	metricHTTPSend  = clientmetric.NewCounter("netcheck_https_measure")
)
