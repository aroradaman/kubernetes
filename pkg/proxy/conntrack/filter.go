//go:build linux
// +build linux

/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package conntrack

import (
	"net"

	"github.com/vishvananda/netlink"
)

// Add custom Layer 4 protocol filter based on:
// https://github.com/vishvananda/netlink/blob/214c19c0cf881d37cf772dea0d6c485bbbecdc26/conntrack_linux.go

// Conntrack parameters and options:
//   -n, --src-nat ip                      source NAT ip
//   -g, --dst-nat ip                      destination NAT ip
//   -j, --any-nat ip                      source or destination NAT ip
//   -m, --mark mark                       Set mark
//   -c, --secmark secmark                 Set selinux secmark
//   -e, --event-mask eventmask            Event mask, eg. NEW,DESTROY
//   -z, --zero                            Zero counters while listing
//   -o, --output type[,...]               Output format, eg. xml
//   -l, --label label[,...]               conntrack labels

// Common parameters and options:
//   -s, --src, --orig-src ip              Source address from original direction
//   -d, --dst, --orig-dst ip              Destination address from original direction
//   -r, --reply-src ip            Source address from reply direction
//   -q, --reply-dst ip            Destination address from reply direction
//   -p, --protonum proto          Layer 4 Protocol, eg. 'tcp'
//   -f, --family proto            Layer 3 Protocol, eg. 'ipv6'
//   -t, --timeout timeout         Set timeout
//   -u, --status status           Set status, eg. ASSURED
//   -w, --zone value              Set conntrack zone
//   --orig-zone value             Set zone for original direction
//   --reply-zone value            Set zone for reply direction
//   -b, --buffer-size             Netlink socket buffer size
//   --mask-src ip                 Source mask address
//   --mask-dst ip                 Destination mask address

// Layer 4 Protocol common parameters and options:
// TCP, UDP, SCTP, UDPLite and DCCP
//    --sport, --orig-port-src port    Source port in original direction
//    --dport, --orig-port-dst port    Destination port in original direction

type connectionTuple struct {
	srcIP   net.IP
	srcPort uint16
	dstIP   net.IP
	dstPort uint16
}

type conntrackFilter struct {
	protocol uint8
	original *connectionTuple
	reply    *connectionTuple
}

var _ netlink.CustomConntrackFilter = (*conntrackFilter)(nil)

// MatchConntrackFlow applies the filter to the flow and returns true if the flow matches the filter
// false otherwise.
func (f *conntrackFilter) MatchConntrackFlow(flow *netlink.ConntrackFlow) bool {
	// return false in case of empty filter
	if f.protocol == 0 && f.original == nil && f.reply == nil {
		return false
	}

	// -p, --protonum proto [Layer 4 Protocol, eg. 'tcp']
	if f.protocol != 0 && f.protocol != flow.Forward.Protocol {
		return false
	}

	// filter on original direction
	if f.original != nil {
		// --orig-src ip  [Source address from original direction]
		if f.original.srcIP != nil && !f.original.srcIP.Equal(flow.Forward.SrcIP) {
			return false
		}
		// --orig-dst ip  [Destination address from original direction]
		if f.original.dstIP != nil && !f.original.dstIP.Equal(flow.Forward.DstIP) {
			return false
		}
		// --orig-port-src port [Source port from original direction]
		if f.original.srcPort != 0 && f.original.srcPort != flow.Forward.SrcPort {
			return false
		}
		// --orig-port-dst port	[Destination port from original direction]
		if f.original.dstPort != 0 && f.original.dstPort != flow.Forward.DstPort {
			return false
		}
	}

	// filter on reply direction
	if f.reply != nil {
		// --src-nat ip  [Source NAT ip]
		if f.reply.srcIP != nil && !f.reply.srcIP.Equal(flow.Reverse.SrcIP) {
			return false
		}
		// --dst-nat ip [Destination NAT ip]
		if f.reply.dstIP != nil && !f.reply.dstIP.Equal(flow.Reverse.DstIP) {
			return false
		}
		// --reply-port-src port [Source port from reply direction]
		if f.reply.srcPort != 0 && f.reply.srcPort != flow.Reverse.SrcPort {
			return false
		}
		// --reply-port-dst port	[Destination port from reply direction]
		if f.reply.dstPort != 0 && f.reply.dstPort != flow.Reverse.DstPort {
			return false
		}
	}
	return true
}
