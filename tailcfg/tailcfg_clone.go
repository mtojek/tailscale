// Copyright (c) 2022 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Code generated by tailscale.com/cmd/cloner; DO NOT EDIT.

package tailcfg

import (
	"net/netip"
	"time"

	"tailscale.com/types/dnstype"
	"tailscale.com/types/key"
	"tailscale.com/types/opt"
	"tailscale.com/types/structs"
)

// Clone makes a deep copy of User.
// The result aliases no memory with the original.
func (src *User) Clone() *User {
	if src == nil {
		return nil
	}
	dst := new(User)
	*dst = *src
	dst.Logins = append(src.Logins[:0:0], src.Logins...)
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _UserCloneNeedsRegeneration = User(struct {
	ID            UserID
	LoginName     string
	DisplayName   string
	ProfilePicURL string
	Domain        string
	Logins        []LoginID
	Created       time.Time
}{})

// Clone makes a deep copy of Node.
// The result aliases no memory with the original.
func (src *Node) Clone() *Node {
	if src == nil {
		return nil
	}
	dst := new(Node)
	*dst = *src
	dst.Addresses = append(src.Addresses[:0:0], src.Addresses...)
	dst.AllowedIPs = append(src.AllowedIPs[:0:0], src.AllowedIPs...)
	dst.Endpoints = append(src.Endpoints[:0:0], src.Endpoints...)
	dst.Hostinfo = src.Hostinfo
	dst.Tags = append(src.Tags[:0:0], src.Tags...)
	dst.PrimaryRoutes = append(src.PrimaryRoutes[:0:0], src.PrimaryRoutes...)
	if dst.LastSeen != nil {
		dst.LastSeen = new(time.Time)
		*dst.LastSeen = *src.LastSeen
	}
	if dst.Online != nil {
		dst.Online = new(bool)
		*dst.Online = *src.Online
	}
	dst.Capabilities = append(src.Capabilities[:0:0], src.Capabilities...)
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _NodeCloneNeedsRegeneration = Node(struct {
	ID                      NodeID
	StableID                StableNodeID
	Name                    string
	User                    UserID
	Sharer                  UserID
	Key                     key.NodePublic
	KeyExpiry               time.Time
	Machine                 key.MachinePublic
	DiscoKey                key.DiscoPublic
	Addresses               []netip.Prefix
	AllowedIPs              []netip.Prefix
	Endpoints               []string
	DERP                    string
	Hostinfo                HostinfoView
	Created                 time.Time
	Tags                    []string
	PrimaryRoutes           []netip.Prefix
	LastSeen                *time.Time
	Online                  *bool
	KeepAlive               bool
	MachineAuthorized       bool
	Capabilities            []string
	ComputedName            string
	computedHostIfDifferent string
	ComputedNameWithHost    string
}{})

// Clone makes a deep copy of Hostinfo.
// The result aliases no memory with the original.
func (src *Hostinfo) Clone() *Hostinfo {
	if src == nil {
		return nil
	}
	dst := new(Hostinfo)
	*dst = *src
	dst.RoutableIPs = append(src.RoutableIPs[:0:0], src.RoutableIPs...)
	dst.RequestTags = append(src.RequestTags[:0:0], src.RequestTags...)
	dst.Services = append(src.Services[:0:0], src.Services...)
	dst.NetInfo = src.NetInfo.Clone()
	dst.SSH_HostKeys = append(src.SSH_HostKeys[:0:0], src.SSH_HostKeys...)
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _HostinfoCloneNeedsRegeneration = Hostinfo(struct {
	IPNVersion    string
	FrontendLogID string
	BackendLogID  string
	OS            string
	OSVersion     string
	Desktop       opt.Bool
	Package       string
	DeviceModel   string
	Hostname      string
	ShieldsUp     bool
	ShareeNode    bool
	GoArch        string
	RoutableIPs   []netip.Prefix
	RequestTags   []string
	Services      []Service
	NetInfo       *NetInfo
	SSH_HostKeys  []string
	Cloud         string
}{})

// Clone makes a deep copy of NetInfo.
// The result aliases no memory with the original.
func (src *NetInfo) Clone() *NetInfo {
	if src == nil {
		return nil
	}
	dst := new(NetInfo)
	*dst = *src
	if dst.DERPLatency != nil {
		dst.DERPLatency = map[string]float64{}
		for k, v := range src.DERPLatency {
			dst.DERPLatency[k] = v
		}
	}
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _NetInfoCloneNeedsRegeneration = NetInfo(struct {
	MappingVariesByDestIP opt.Bool
	HairPinning           opt.Bool
	WorkingIPv6           opt.Bool
	OSHasIPv6             opt.Bool
	WorkingUDP            opt.Bool
	HavePortMap           bool
	UPnP                  opt.Bool
	PMP                   opt.Bool
	PCP                   opt.Bool
	PreferredDERP         int
	LinkType              string
	DERPLatency           map[string]float64
}{})

// Clone makes a deep copy of Login.
// The result aliases no memory with the original.
func (src *Login) Clone() *Login {
	if src == nil {
		return nil
	}
	dst := new(Login)
	*dst = *src
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _LoginCloneNeedsRegeneration = Login(struct {
	_             structs.Incomparable
	ID            LoginID
	Provider      string
	LoginName     string
	DisplayName   string
	ProfilePicURL string
	Domain        string
}{})

// Clone makes a deep copy of DNSConfig.
// The result aliases no memory with the original.
func (src *DNSConfig) Clone() *DNSConfig {
	if src == nil {
		return nil
	}
	dst := new(DNSConfig)
	*dst = *src
	dst.Resolvers = make([]*dnstype.Resolver, len(src.Resolvers))
	for i := range dst.Resolvers {
		dst.Resolvers[i] = src.Resolvers[i].Clone()
	}
	if dst.Routes != nil {
		dst.Routes = map[string][]*dnstype.Resolver{}
		for k := range src.Routes {
			dst.Routes[k] = append([]*dnstype.Resolver{}, src.Routes[k]...)
		}
	}
	dst.FallbackResolvers = make([]*dnstype.Resolver, len(src.FallbackResolvers))
	for i := range dst.FallbackResolvers {
		dst.FallbackResolvers[i] = src.FallbackResolvers[i].Clone()
	}
	dst.Domains = append(src.Domains[:0:0], src.Domains...)
	dst.Nameservers = append(src.Nameservers[:0:0], src.Nameservers...)
	dst.CertDomains = append(src.CertDomains[:0:0], src.CertDomains...)
	dst.ExtraRecords = append(src.ExtraRecords[:0:0], src.ExtraRecords...)
	dst.ExitNodeFilteredSet = append(src.ExitNodeFilteredSet[:0:0], src.ExitNodeFilteredSet...)
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _DNSConfigCloneNeedsRegeneration = DNSConfig(struct {
	Resolvers           []*dnstype.Resolver
	Routes              map[string][]*dnstype.Resolver
	FallbackResolvers   []*dnstype.Resolver
	Domains             []string
	Proxied             bool
	Nameservers         []netip.Addr
	PerDomain           bool
	CertDomains         []string
	ExtraRecords        []DNSRecord
	ExitNodeFilteredSet []string
}{})

// Clone makes a deep copy of RegisterResponse.
// The result aliases no memory with the original.
func (src *RegisterResponse) Clone() *RegisterResponse {
	if src == nil {
		return nil
	}
	dst := new(RegisterResponse)
	*dst = *src
	dst.User = *src.User.Clone()
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _RegisterResponseCloneNeedsRegeneration = RegisterResponse(struct {
	User              User
	Login             Login
	NodeKeyExpired    bool
	MachineAuthorized bool
	AuthURL           string
	Error             string
}{})

// Clone makes a deep copy of DERPRegion.
// The result aliases no memory with the original.
func (src *DERPRegion) Clone() *DERPRegion {
	if src == nil {
		return nil
	}
	dst := new(DERPRegion)
	*dst = *src
	dst.Nodes = make([]*DERPNode, len(src.Nodes))
	for i := range dst.Nodes {
		dst.Nodes[i] = src.Nodes[i].Clone()
	}
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _DERPRegionCloneNeedsRegeneration = DERPRegion(struct {
	RegionID   int
	RegionCode string
	RegionName string
	Avoid      bool
	Nodes      []*DERPNode
}{})

// Clone makes a deep copy of DERPMap.
// The result aliases no memory with the original.
func (src *DERPMap) Clone() *DERPMap {
	if src == nil {
		return nil
	}
	dst := new(DERPMap)
	*dst = *src
	if dst.Regions != nil {
		dst.Regions = map[int]*DERPRegion{}
		for k, v := range src.Regions {
			dst.Regions[k] = v.Clone()
		}
	}
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _DERPMapCloneNeedsRegeneration = DERPMap(struct {
	Regions            map[int]*DERPRegion
	OmitDefaultRegions bool
}{})

// Clone makes a deep copy of DERPNode.
// The result aliases no memory with the original.
func (src *DERPNode) Clone() *DERPNode {
	if src == nil {
		return nil
	}
	dst := new(DERPNode)
	*dst = *src
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _DERPNodeCloneNeedsRegeneration = DERPNode(struct {
	Name             string
	RegionID         int
	HostName         string
	CertName         string
	IPv4             string
	IPv6             string
	STUNPort         int
	STUNOnly         bool
	DERPPort         int
	InsecureForTests bool
	HTTPForTests     bool
	STUNTestIP       string
}{})

// Clone makes a deep copy of SSHRule.
// The result aliases no memory with the original.
func (src *SSHRule) Clone() *SSHRule {
	if src == nil {
		return nil
	}
	dst := new(SSHRule)
	*dst = *src
	if dst.RuleExpires != nil {
		dst.RuleExpires = new(time.Time)
		*dst.RuleExpires = *src.RuleExpires
	}
	dst.Principals = make([]*SSHPrincipal, len(src.Principals))
	for i := range dst.Principals {
		dst.Principals[i] = src.Principals[i].Clone()
	}
	if dst.SSHUsers != nil {
		dst.SSHUsers = map[string]string{}
		for k, v := range src.SSHUsers {
			dst.SSHUsers[k] = v
		}
	}
	if dst.Action != nil {
		dst.Action = new(SSHAction)
		*dst.Action = *src.Action
	}
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _SSHRuleCloneNeedsRegeneration = SSHRule(struct {
	RuleExpires *time.Time
	Principals  []*SSHPrincipal
	SSHUsers    map[string]string
	Action      *SSHAction
}{})

// Clone makes a deep copy of SSHPrincipal.
// The result aliases no memory with the original.
func (src *SSHPrincipal) Clone() *SSHPrincipal {
	if src == nil {
		return nil
	}
	dst := new(SSHPrincipal)
	*dst = *src
	dst.PubKeys = append(src.PubKeys[:0:0], src.PubKeys...)
	return dst
}

// A compilation failure here means this code must be regenerated, with the command at the top of this file.
var _SSHPrincipalCloneNeedsRegeneration = SSHPrincipal(struct {
	Node      StableNodeID
	NodeIP    string
	UserLogin string
	Any       bool
	PubKeys   []string
}{})

// Clone duplicates src into dst and reports whether it succeeded.
// To succeed, <src, dst> must be of types <*T, *T> or <*T, **T>,
// where T is one of User,Node,Hostinfo,NetInfo,Login,DNSConfig,RegisterResponse,DERPRegion,DERPMap,DERPNode,SSHRule,SSHPrincipal.
func Clone(dst, src any) bool {
	switch src := src.(type) {
	case *User:
		switch dst := dst.(type) {
		case *User:
			*dst = *src.Clone()
			return true
		case **User:
			*dst = src.Clone()
			return true
		}
	case *Node:
		switch dst := dst.(type) {
		case *Node:
			*dst = *src.Clone()
			return true
		case **Node:
			*dst = src.Clone()
			return true
		}
	case *Hostinfo:
		switch dst := dst.(type) {
		case *Hostinfo:
			*dst = *src.Clone()
			return true
		case **Hostinfo:
			*dst = src.Clone()
			return true
		}
	case *NetInfo:
		switch dst := dst.(type) {
		case *NetInfo:
			*dst = *src.Clone()
			return true
		case **NetInfo:
			*dst = src.Clone()
			return true
		}
	case *Login:
		switch dst := dst.(type) {
		case *Login:
			*dst = *src.Clone()
			return true
		case **Login:
			*dst = src.Clone()
			return true
		}
	case *DNSConfig:
		switch dst := dst.(type) {
		case *DNSConfig:
			*dst = *src.Clone()
			return true
		case **DNSConfig:
			*dst = src.Clone()
			return true
		}
	case *RegisterResponse:
		switch dst := dst.(type) {
		case *RegisterResponse:
			*dst = *src.Clone()
			return true
		case **RegisterResponse:
			*dst = src.Clone()
			return true
		}
	case *DERPRegion:
		switch dst := dst.(type) {
		case *DERPRegion:
			*dst = *src.Clone()
			return true
		case **DERPRegion:
			*dst = src.Clone()
			return true
		}
	case *DERPMap:
		switch dst := dst.(type) {
		case *DERPMap:
			*dst = *src.Clone()
			return true
		case **DERPMap:
			*dst = src.Clone()
			return true
		}
	case *DERPNode:
		switch dst := dst.(type) {
		case *DERPNode:
			*dst = *src.Clone()
			return true
		case **DERPNode:
			*dst = src.Clone()
			return true
		}
	case *SSHRule:
		switch dst := dst.(type) {
		case *SSHRule:
			*dst = *src.Clone()
			return true
		case **SSHRule:
			*dst = src.Clone()
			return true
		}
	case *SSHPrincipal:
		switch dst := dst.(type) {
		case *SSHPrincipal:
			*dst = *src.Clone()
			return true
		case **SSHPrincipal:
			*dst = src.Clone()
			return true
		}
	}
	return false
}
