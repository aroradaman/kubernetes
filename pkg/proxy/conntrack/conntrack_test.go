//go:build linux
// +build linux

/*
Copyright 2015 The Kubernetes Authors.

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
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	v1 "k8s.io/api/core/v1"
	netutils "k8s.io/utils/net"
)

var (
	testClientPodIP        = net.IP{10, 244, 10, 10}
	testClientPort  uint16 = 54321

	testServiceIP          = net.IP{10, 96, 10, 10}
	testServicePort uint16 = 3000

	testServerPodIP          = net.IP{10, 244, 20, 20}
	testServerPodPort uint16 = 5000
)

func skipUnlessRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("Test requires root privileges.")
	}
}

// creteConntrackFlow creates a dummy conntrack flow entry for testing.
func creteConntrackFlow() error {
	return netlink.ConntrackCreate(netlink.ConntrackTable, unix.AF_INET, &netlink.ConntrackFlow{
		FamilyType: unix.AF_INET,
		Forward: netlink.IPTuple{
			Protocol: unix.IPPROTO_UDP,
			SrcIP:    testClientPodIP,
			SrcPort:  testClientPort,
			DstIP:    testServiceIP,
			DstPort:  testServicePort,
		},
		Reverse: netlink.IPTuple{
			Protocol: unix.IPPROTO_UDP,
			SrcIP:    testServerPodIP,
			SrcPort:  testServerPodPort,
			DstIP:    testClientPodIP,
			DstPort:  testClientPort,
		},
		TimeOut: 120,
	})
}

// flowExists returns true if dummy conntrack flow entry exists, false otherwise.
func flowExists() (bool, error) {
	flows, err := netlink.ConntrackTableList(netlink.ConntrackTable, unix.AF_INET)
	if err != nil {
		return false, err
	}
	for _, flow := range flows {
		if flow.FamilyType == unix.AF_INET &&
			flow.Forward.SrcIP.Equal(testClientPodIP) &&
			flow.Forward.SrcPort == testClientPort &&
			flow.Forward.DstIP.Equal(testServiceIP) &&
			flow.Forward.DstPort == testServicePort &&
			flow.Forward.Protocol == unix.IPPROTO_UDP &&
			flow.Reverse.SrcIP.Equal(testServerPodIP) &&
			flow.Reverse.SrcPort == testServerPodPort &&
			flow.Reverse.DstIP.Equal(testClientPodIP) &&
			flow.Reverse.DstPort == testClientPort &&
			flow.Reverse.Protocol == unix.IPPROTO_UDP {
			return true, nil
		}
	}
	return false, nil
}

type fakeHandler struct {
	tableType netlink.ConntrackTableType
	family    netlink.InetFamily
	filter    *conntrackFilter
}

func (f *fakeHandler) ConntrackDeleteFilters(tableType netlink.ConntrackTableType, family netlink.InetFamily, filters ...netlink.CustomConntrackFilter) (uint, error) {
	f.tableType = tableType
	f.family = family
	f.filter = filters[0].(*conntrackFilter)
	return 1, nil
}

var _ netlinkHandler = (*fakeHandler)(nil)

func TestConntracker_ClearEntriesForIP(t *testing.T) {
	testCases := []struct {
		name           string
		ip             string
		protocol       v1.Protocol
		expectedFamily netlink.InetFamily
		expectedFilter *conntrackFilter
	}{
		{
			name:           "ipv4 + UDP",
			ip:             "10.96.0.10",
			protocol:       v1.ProtocolUDP,
			expectedFamily: unix.AF_INET,
			expectedFilter: &conntrackFilter{
				protocol: 17,
				original: &connectionTuple{dstIP: netutils.ParseIPSloppy("10.96.0.10")},
			},
		},
		{
			name:           "ipv6 + TCP",
			ip:             "2001:db8:1::2",
			protocol:       v1.ProtocolTCP,
			expectedFamily: unix.AF_INET6,
			expectedFilter: &conntrackFilter{
				protocol: 6,
				original: &connectionTuple{dstIP: netutils.ParseIPSloppy("2001:db8:1::2")},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := &fakeHandler{}
			ct := newConntracker(handler)
			require.NoError(t, ct.ClearEntriesForIP(tc.ip, tc.protocol))
			require.Equal(t, netlink.ConntrackTableType(netlink.ConntrackTable), handler.tableType)
			require.Equal(t, tc.expectedFamily, handler.family)
			require.Equal(t, tc.expectedFilter, handler.filter)
		})
	}

	// create actual conntrack flow in kernel and test deleting it via API
	t.Run("actual", func(t *testing.T) {
		skipUnlessRoot(t)
		// create test conntrack flow entry
		require.NoError(t, creteConntrackFlow())

		ct := NewConntracker()

		// ensure flow exists
		exists, err := flowExists()
		require.NoError(t, err, "failed to list conntrack flows")
		require.True(t, exists, "expected test flow to exist")

		// invoke API
		require.NoError(t, ct.ClearEntriesForIP(testServiceIP.String(), v1.ProtocolUDP))

		// ensure flow doesn't exist
		exists, err = flowExists()
		require.NoError(t, err, "failed to list conntrack flows")
		require.False(t, exists, "expected test flow to not exist")
	})
}

func TestConntracker_ClearEntriesForPort(t *testing.T) {
	testCases := []struct {
		name           string
		port           int
		isIPv6         bool
		protocol       v1.Protocol
		expectedFamily netlink.InetFamily
		expectedFilter *conntrackFilter
	}{
		{
			name:           "ipv4 + UDP",
			port:           5000,
			isIPv6:         false,
			protocol:       v1.ProtocolUDP,
			expectedFamily: unix.AF_INET,
			expectedFilter: &conntrackFilter{
				protocol: 17,
				original: &connectionTuple{dstPort: 5000},
			},
		},
		{
			name:           "ipv6 + SCTP",
			port:           3000,
			isIPv6:         true,
			protocol:       v1.ProtocolSCTP,
			expectedFamily: unix.AF_INET6,
			expectedFilter: &conntrackFilter{
				protocol: 132,
				original: &connectionTuple{dstPort: 3000},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := &fakeHandler{}
			ct := newConntracker(handler)
			require.NoError(t, ct.ClearEntriesForPort(tc.port, tc.isIPv6, tc.protocol))
			require.Equal(t, netlink.ConntrackTableType(netlink.ConntrackTable), handler.tableType)
			require.Equal(t, tc.expectedFamily, handler.family)
			require.Equal(t, tc.expectedFilter, handler.filter)
		})
	}

	// create actual conntrack flow in kernel and test deleting it via API
	t.Run("actual", func(t *testing.T) {
		skipUnlessRoot(t)
		// create test conntrack flow entry
		require.NoError(t, creteConntrackFlow())

		ct := NewConntracker()

		// ensure flow exists
		exists, err := flowExists()
		require.NoError(t, err, "failed to list conntrack flows")
		require.True(t, exists, "expected test flow to exist")

		// invoke API
		require.NoError(t, ct.ClearEntriesForPort(int(testServicePort), false, v1.ProtocolUDP))

		// ensure flow doesn't exist
		exists, err = flowExists()
		require.NoError(t, err, "failed to list conntrack flows")
		require.False(t, exists, "expected test flow to not exist")
	})
}

func TestConntracker_ClearEntriesForNAT(t *testing.T) {
	testCases := []struct {
		name           string
		src            string
		dest           string
		protocol       v1.Protocol
		expectedFamily netlink.InetFamily
		expectedFilter *conntrackFilter
	}{
		{
			name:           "ipv4 + SCTP",
			src:            "10.96.0.10",
			dest:           "10.244.0.3",
			protocol:       v1.ProtocolSCTP,
			expectedFamily: unix.AF_INET,
			expectedFilter: &conntrackFilter{
				protocol: 132,
				original: &connectionTuple{dstIP: netutils.ParseIPSloppy("10.96.0.10")},
				reply:    &connectionTuple{srcIP: netutils.ParseIPSloppy("10.244.0.3")},
			},
		},
		{
			name:           "ipv6 + UDP",
			src:            "2001:db8:1::2",
			dest:           "4001:ab8::2",
			protocol:       v1.ProtocolUDP,
			expectedFamily: unix.AF_INET6,
			expectedFilter: &conntrackFilter{
				protocol: 17,
				original: &connectionTuple{dstIP: netutils.ParseIPSloppy("2001:db8:1::2")},
				reply:    &connectionTuple{srcIP: netutils.ParseIPSloppy("4001:ab8::2")},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := &fakeHandler{}
			ct := newConntracker(handler)
			require.NoError(t, ct.ClearEntriesForNAT(tc.src, tc.dest, tc.protocol))
			require.Equal(t, netlink.ConntrackTableType(netlink.ConntrackTable), handler.tableType)
			require.Equal(t, tc.expectedFamily, handler.family)
			require.Equal(t, tc.expectedFilter, handler.filter)
		})
	}

	// create actual conntrack flow in kernel and test deleting it via API
	t.Run("actual", func(t *testing.T) {
		skipUnlessRoot(t)
		// create test conntrack flow entry
		require.NoError(t, creteConntrackFlow())

		ct := NewConntracker()

		// ensure flow exists
		exists, err := flowExists()
		require.NoError(t, err, "failed to list conntrack flows")
		require.True(t, exists, "expected test flow to exist")

		// invoke API
		require.NoError(t, ct.ClearEntriesForNAT(testServiceIP.String(), testServerPodIP.String(), v1.ProtocolUDP))

		// ensure flow doesn't exist
		exists, err = flowExists()
		require.NoError(t, err, "failed to list conntrack flows")
		require.False(t, exists, "expected test flow to not exist")
	})
}

func TestConntracker_ClearEntriesForPortNAT(t *testing.T) {
	testCases := []struct {
		name           string
		ip             string
		port           int
		protocol       v1.Protocol
		expectedFamily netlink.InetFamily
		expectedFilter *conntrackFilter
	}{
		{
			name:           "ipv4 + TCP",
			ip:             "10.96.0.10",
			port:           80,
			protocol:       v1.ProtocolTCP,
			expectedFamily: unix.AF_INET,
			expectedFilter: &conntrackFilter{
				protocol: 6,
				original: &connectionTuple{dstPort: 80},
				reply:    &connectionTuple{srcIP: netutils.ParseIPSloppy("10.96.0.10")},
			},
		},
		{
			name:           "ipv6 + UDP",
			ip:             "2001:db8:1::2",
			port:           8000,
			protocol:       v1.ProtocolUDP,
			expectedFamily: unix.AF_INET6,
			expectedFilter: &conntrackFilter{
				protocol: 17,
				original: &connectionTuple{dstPort: 8000},
				reply:    &connectionTuple{srcIP: netutils.ParseIPSloppy("2001:db8:1::2")},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := &fakeHandler{}
			ct := newConntracker(handler)
			require.NoError(t, ct.ClearEntriesForPortNAT(tc.ip, tc.port, tc.protocol))
			require.Equal(t, netlink.ConntrackTableType(netlink.ConntrackTable), handler.tableType)
			require.Equal(t, tc.expectedFamily, handler.family)
			require.Equal(t, tc.expectedFilter, handler.filter)
		})
	}

	// create actual conntrack flow in kernel and test deleting it via API
	t.Run("actual", func(t *testing.T) {
		skipUnlessRoot(t)
		// create test conntrack flow entry
		require.NoError(t, creteConntrackFlow())

		ct := NewConntracker()

		// ensure flow exists
		exists, err := flowExists()
		require.NoError(t, err, "failed to list conntrack flows")
		require.True(t, exists, "expected test flow to exist")

		// invoke API
		require.NoError(t, ct.ClearEntriesForPortNAT(testServerPodIP.String(), int(testServicePort), v1.ProtocolUDP))

		// ensure flow doesn't exist
		exists, err = flowExists()
		require.NoError(t, err, "failed to list conntrack flows")
		require.False(t, exists, "expected test flow to not exist")
	})
}
