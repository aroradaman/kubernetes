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
	netutils "k8s.io/utils/net"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"

	v1 "k8s.io/api/core/v1"
)

func TestConntracker_ClearEntriesForIP(t *testing.T) {
	testCases := []struct {
		name           string
		ip             string
		protocol       v1.Protocol
		expectedFamily netlink.InetFamily
		expectedFilter *conntrackFilter
	}{
		{
			name:           "IPv4 TCP",
			ip:             "10.20.30.40",
			protocol:       v1.ProtocolTCP,
			expectedFamily: netlink.FAMILY_V4,
			expectedFilter: &conntrackFilter{
				ipFilter: map[conntrackFilterType]net.IP{
					conntrackOrigDstIP: netutils.ParseIPSloppy("10.20.30.40"),
				},
				protoFilter: PROTOCOL_TCP,
			},
		},
		{
			name:           "IPv6 UDP",
			ip:             "18ad:7b90:bcde:fdba:b67f:f3b1:9892:bf49",
			protocol:       v1.ProtocolUDP,
			expectedFamily: netlink.FAMILY_V6,
			expectedFilter: &conntrackFilter{
				ipFilter: map[conntrackFilterType]net.IP{
					conntrackOrigDstIP: netutils.ParseIPSloppy("18ad:7b90:bcde:fdba:b67f:f3b1:9892:bf49"),
				},
				protoFilter: PROTOCOL_UDP,
			},
		},
		{
			name:           "IPv4 SCTP",
			ip:             "192.168.15.25",
			protocol:       v1.ProtocolSCTP,
			expectedFamily: netlink.FAMILY_V4,
			expectedFilter: &conntrackFilter{
				ipFilter: map[conntrackFilterType]net.IP{
					conntrackOrigDstIP: netutils.ParseIPSloppy("192.168.15.25"),
				},
				protoFilter: PROTOCOL_SCTP,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actualTableType netlink.ConntrackTableType
			var actualFamily netlink.InetFamily
			var actualFilter *conntrackFilter

			flowDeleteFunc := func(tableType netlink.ConntrackTableType, family netlink.InetFamily, filter netlink.CustomConntrackFilter) (uint, error) {
				actualTableType = tableType
				actualFamily = family
				actualFilter = filter.(*conntrackFilter)
				return 0, nil
			}
			ct := newConntracker(flowDeleteFunc)
			_ = ct.ClearEntriesForIP(tc.ip, tc.protocol)

			require.Equal(t, netlink.ConntrackTable, int(actualTableType))
			require.Equal(t, tc.expectedFamily, actualFamily)
			require.Equal(t, tc.expectedFilter, actualFilter)
		})
	}
}

func TestConntracker_ClearEntriesForPort(t *testing.T) {
	testCases := []struct {
		name           string
		port           int
		protocol       v1.Protocol
		isIPv6         bool
		expectedFamily netlink.InetFamily
		expectedFilter *conntrackFilter
	}{
		{
			name:           "IPv4 TCP",
			port:           80,
			protocol:       v1.ProtocolTCP,
			isIPv6:         false,
			expectedFamily: netlink.FAMILY_V4,
			expectedFilter: &conntrackFilter{
				portFilter: map[conntrackFilterType]uint16{
					conntrackOrigDstPort: 80,
				},
				protoFilter: PROTOCOL_TCP,
			},
		},
		{
			name:           "IPv6 UDP",
			port:           3000,
			protocol:       v1.ProtocolUDP,
			isIPv6:         true,
			expectedFamily: netlink.FAMILY_V6,
			expectedFilter: &conntrackFilter{
				portFilter: map[conntrackFilterType]uint16{
					conntrackOrigDstPort: 3000,
				},
				protoFilter: PROTOCOL_UDP,
			},
		},
		{
			name:           "IPv4 SCTP",
			port:           5000,
			protocol:       v1.ProtocolSCTP,
			isIPv6:         false,
			expectedFamily: netlink.FAMILY_V4,
			expectedFilter: &conntrackFilter{
				portFilter: map[conntrackFilterType]uint16{
					conntrackOrigDstPort: 5000,
				},
				protoFilter: PROTOCOL_SCTP,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actualTableType netlink.ConntrackTableType
			var actualFamily netlink.InetFamily
			var actualFilter *conntrackFilter

			flowDeleteFunc := func(tableType netlink.ConntrackTableType, family netlink.InetFamily, filter netlink.CustomConntrackFilter) (uint, error) {
				actualTableType = tableType
				actualFamily = family
				actualFilter = filter.(*conntrackFilter)
				return 0, nil
			}
			ct := newConntracker(flowDeleteFunc)
			_ = ct.ClearEntriesForPort(tc.port, tc.isIPv6, tc.protocol)

			require.Equal(t, netlink.ConntrackTable, int(actualTableType))
			require.Equal(t, tc.expectedFamily, actualFamily)
			require.Equal(t, tc.expectedFilter, actualFilter)
		})
	}
}

func TestConntracker_ClearEntriesForNAT(t *testing.T) {
	testCases := []struct {
		name           string
		origin         string
		dest           string
		protocol       v1.Protocol
		isIPv6         bool
		expectedFamily netlink.InetFamily
		expectedFilter *conntrackFilter
	}{
		{
			name:           "IPv4 TCP",
			origin:         "10.10.10.10",
			dest:           "192.168.50.100",
			protocol:       v1.ProtocolTCP,
			isIPv6:         false,
			expectedFamily: netlink.FAMILY_V4,
			expectedFilter: &conntrackFilter{
				ipFilter: map[conntrackFilterType]net.IP{
					conntrackOrigDstIP:  netutils.ParseIPSloppy("10.10.10.10"),
					conntrackReplyDstIP: netutils.ParseIPSloppy("192.168.50.100"),
				},
				protoFilter: PROTOCOL_TCP,
			},
		},
		{
			name:           "IPv6 UDP",
			origin:         "86c8:f2b7:1c3c:9c79:434e:4d1b:1b7d:2e59",
			dest:           "330f:486a:f77d:7afd:36ab:0d8d:237e:42a8",
			protocol:       v1.ProtocolUDP,
			isIPv6:         true,
			expectedFamily: netlink.FAMILY_V6,
			expectedFilter: &conntrackFilter{
				ipFilter: map[conntrackFilterType]net.IP{
					conntrackOrigDstIP:  netutils.ParseIPSloppy("86c8:f2b7:1c3c:9c79:434e:4d1b:1b7d:2e59"),
					conntrackReplyDstIP: netutils.ParseIPSloppy("330f:486a:f77d:7afd:36ab:0d8d:237e:42a8"),
				},
				protoFilter: PROTOCOL_UDP,
			},
		},
		{
			name:           "IPv4 SCTP",
			origin:         "172.17.5.5",
			dest:           "10.20.30.40",
			protocol:       v1.ProtocolSCTP,
			isIPv6:         false,
			expectedFamily: netlink.FAMILY_V4,
			expectedFilter: &conntrackFilter{
				ipFilter: map[conntrackFilterType]net.IP{
					conntrackOrigDstIP:  netutils.ParseIPSloppy("172.17.5.5"),
					conntrackReplyDstIP: netutils.ParseIPSloppy("10.20.30.40"),
				},
				protoFilter: PROTOCOL_SCTP,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actualTableType netlink.ConntrackTableType
			var actualFamily netlink.InetFamily
			var actualFilter *conntrackFilter

			flowDeleteFunc := func(tableType netlink.ConntrackTableType, family netlink.InetFamily, filter netlink.CustomConntrackFilter) (uint, error) {
				actualTableType = tableType
				actualFamily = family
				actualFilter = filter.(*conntrackFilter)
				return 0, nil
			}
			ct := newConntracker(flowDeleteFunc)
			_ = ct.ClearEntriesForNAT(tc.origin, tc.dest, tc.protocol)

			require.Equal(t, netlink.ConntrackTable, int(actualTableType))
			require.Equal(t, tc.expectedFamily, actualFamily)
			require.Equal(t, tc.expectedFilter, actualFilter)
		})
	}
}

func TestConntracker_ClearEntriesForPortNAT(t *testing.T) {
	testCases := []struct {
		name           string
		ip             string
		port           int
		protocol       v1.Protocol
		isIPv6         bool
		expectedFamily netlink.InetFamily
		expectedFilter *conntrackFilter
	}{
		{
			name:           "IPv4 TCP",
			ip:             "10.10.10.10",
			port:           3000,
			protocol:       v1.ProtocolTCP,
			isIPv6:         false,
			expectedFamily: netlink.FAMILY_V4,
			expectedFilter: &conntrackFilter{
				ipFilter: map[conntrackFilterType]net.IP{
					conntrackReplyDstIP: netutils.ParseIPSloppy("10.10.10.10"),
				},
				portFilter: map[conntrackFilterType]uint16{
					conntrackOrigDstPort: 3000,
				},
				protoFilter: PROTOCOL_TCP,
			},
		},
		{
			name:           "IPv6 UDP",
			ip:             "86c8:f2b7:1c3c:9c79:434e:4d1b:1b7d:2e59",
			port:           5000,
			protocol:       v1.ProtocolUDP,
			isIPv6:         true,
			expectedFamily: netlink.FAMILY_V6,
			expectedFilter: &conntrackFilter{
				ipFilter: map[conntrackFilterType]net.IP{
					conntrackReplyDstIP: netutils.ParseIPSloppy("86c8:f2b7:1c3c:9c79:434e:4d1b:1b7d:2e59"),
				},
				portFilter: map[conntrackFilterType]uint16{
					conntrackOrigDstPort: 5000,
				},
				protoFilter: PROTOCOL_UDP,
			},
		},
		{
			name:           "IPv4 SCTP",
			ip:             "172.17.5.5",
			port:           8080,
			protocol:       v1.ProtocolSCTP,
			isIPv6:         false,
			expectedFamily: netlink.FAMILY_V4,
			expectedFilter: &conntrackFilter{
				ipFilter: map[conntrackFilterType]net.IP{
					conntrackReplyDstIP: netutils.ParseIPSloppy("172.17.5.5"),
				},
				portFilter: map[conntrackFilterType]uint16{
					conntrackOrigDstPort: 8080,
				},
				protoFilter: PROTOCOL_SCTP,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actualTableType netlink.ConntrackTableType
			var actualFamily netlink.InetFamily
			var actualFilter *conntrackFilter

			flowDeleteFunc := func(tableType netlink.ConntrackTableType, family netlink.InetFamily, filter netlink.CustomConntrackFilter) (uint, error) {
				actualTableType = tableType
				actualFamily = family
				actualFilter = filter.(*conntrackFilter)
				return 0, nil
			}
			ct := newConntracker(flowDeleteFunc)
			_ = ct.ClearEntriesForPortNAT(tc.ip, tc.port, tc.protocol)

			require.Equal(t, netlink.ConntrackTable, int(actualTableType))
			require.Equal(t, tc.expectedFamily, actualFamily)
			require.Equal(t, tc.expectedFilter, actualFilter)
		})
	}
}
