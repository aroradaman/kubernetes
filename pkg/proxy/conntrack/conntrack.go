//go:build linux
// +build linux

/*
Copyright 2016 The Kubernetes Authors.

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
	"fmt"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	netutils "k8s.io/utils/net"
)

// Interface for dealing with conntrack
type Interface interface {
	// ClearEntriesForIP deletes conntrack entries for connections of the given
	// protocol, to the given IP.
	ClearEntriesForIP(ip string, protocol v1.Protocol) error

	// ClearEntriesForPort deletes conntrack entries for connections of the given
	// protocol and IP family, to the given port.
	ClearEntriesForPort(port int, isIPv6 bool, protocol v1.Protocol) error

	// ClearEntriesForNAT deletes conntrack entries for connections of the given
	// protocol, which had been DNATted from origin to dest.
	ClearEntriesForNAT(origin, dest string, protocol v1.Protocol) error

	// ClearEntriesForPortNAT deletes conntrack entries for connections of the given
	// protocol, which had been DNATted from the given port (on any IP) to dest.
	ClearEntriesForPortNAT(dest string, port int, protocol v1.Protocol) error
}

// netlinkHandler allows consuming real and mockable implementation for testing.
type netlinkHandler interface {
	ConntrackDeleteFilters(netlink.ConntrackTableType, netlink.InetFamily, ...netlink.CustomConntrackFilter) (uint, error)
}

// conntracker implements Interface by execing the conntrack tool
type conntracker struct {
	handler netlinkHandler
}

var _ Interface = &conntracker{}

func NewConntracker() Interface {
	return newConntracker(&netlink.Handle{})
}

func newConntracker(handler netlinkHandler) Interface {
	return &conntracker{handler: handler}
}

func protoStr(proto v1.Protocol) string {
	return strings.ToLower(string(proto))
}

// getNetlinkFamily returns the Netlink IP family constant
func getNetlinkFamily(isIPv6 bool) netlink.InetFamily {
	if isIPv6 {
		return unix.AF_INET6
	}
	return unix.AF_INET
}

// getProtocolNumber return the Assigned Internet Protocol Number.
// https://www.iana.org/assignments/protocol-numbers/protocol-numbers.xhtml
func getProtocolNumber(proto v1.Protocol) uint8 {
	switch proto {
	case v1.ProtocolTCP:
		return unix.IPPROTO_TCP
	case v1.ProtocolUDP:
		return unix.IPPROTO_UDP
	case v1.ProtocolSCTP:
		return unix.IPPROTO_SCTP
	}
	return 0
}

// ClearEntriesForIP is part of Interface
func (ct *conntracker) ClearEntriesForIP(ip string, protocol v1.Protocol) error {
	filter := &conntrackFilter{
		protocol: getProtocolNumber(protocol),
		original: &connectionTuple{
			dstIP: netutils.ParseIPSloppy(ip),
		},
	}
	klog.V(4).InfoS("Clearing conntrack entries", "ip", ip, "protocol", protocol)

	n, err := ct.handler.ConntrackDeleteFilters(netlink.ConntrackTable, getNetlinkFamily(netutils.IsIPv6String(ip)), filter)
	if err != nil {
		// TODO: Better handling for deletion failure. When failure occur, stale udp connection may not get flushed.
		// These stale udp connection will keep black hole traffic. Making this a best effort operation for now, since it
		// is expensive to baby-sit all udp connections to kubernetes services.
		return fmt.Errorf("error deleting connection tracking state for %s service IP: %s, error: %w", protoStr(protocol), ip, err)
	}
	klog.V(4).InfoS("Cleared conntrack entries", "count", n)
	return nil
}

// ClearEntriesForPort delete the conntrack entries for connections specified by the port.
func (ct *conntracker) ClearEntriesForPort(port int, isIPv6 bool, protocol v1.Protocol) error {
	filter := &conntrackFilter{
		protocol: getProtocolNumber(protocol),
		original: &connectionTuple{
			dstPort: uint16(port),
		},
	}
	if port <= 0 {
		return fmt.Errorf("wrong port number. The port number must be greater than zero")
	}

	klog.V(4).InfoS("Clearing conntrack entries", "port", port, "protocol", protocol)
	n, err := ct.handler.ConntrackDeleteFilters(netlink.ConntrackTable, getNetlinkFamily(isIPv6), filter)
	if err != nil {
		return fmt.Errorf("error deleting connection tracking state for %s port: %d, error: %w", protoStr(protocol), port, err)
	}
	if n == 0 {
		return fmt.Errorf("error deleting connection tracking state for %s port: %d, no entries found", protoStr(protocol), port)
	}
	return nil
}

// ClearEntriesForNAT is part of Interface
func (ct *conntracker) ClearEntriesForNAT(origin, dest string, protocol v1.Protocol) error {
	filter := &conntrackFilter{
		protocol: getProtocolNumber(protocol),
		original: &connectionTuple{
			dstIP: netutils.ParseIPSloppy(origin),
		},
		reply: &connectionTuple{
			srcIP: netutils.ParseIPSloppy(dest),
		},
	}

	klog.V(4).InfoS("Clearing conntrack entries", "origin", origin, "destination", dest, "protocol", protocol)
	n, err := ct.handler.ConntrackDeleteFilters(netlink.ConntrackTable, getNetlinkFamily(netutils.IsIPv6String(origin)), filter)
	if err != nil {
		// TODO: Better handling for deletion failure. When failure occur, stale udp connection may not get flushed.
		// These stale udp connection will keep black hole traffic. Making this a best effort operation for now, since it
		// is expensive to baby sit all udp connections to kubernetes services.
		return fmt.Errorf("error deleting conntrack entries for %s peer {%s, %s}, error: %w", protoStr(protocol), origin, dest, err)
	}
	klog.V(4).InfoS("Cleared conntrack entries", "count", n)
	return nil
}

// ClearEntriesForPortNAT is part of Interface
func (ct *conntracker) ClearEntriesForPortNAT(dest string, port int, protocol v1.Protocol) error {
	if port <= 0 {
		return fmt.Errorf("wrong port number. The port number must be greater than zero")
	}
	filter := &conntrackFilter{
		protocol: getProtocolNumber(protocol),
		original: &connectionTuple{
			dstPort: uint16(port),
		},
		reply: &connectionTuple{
			srcIP: netutils.ParseIPSloppy(dest),
		},
	}
	klog.V(4).InfoS("Clearing conntrack entries", "destination", dest, "port", port, "protocol", protocol)
	n, err := ct.handler.ConntrackDeleteFilters(netlink.ConntrackTable, getNetlinkFamily(netutils.IsIPv6String(dest)), filter)
	if err != nil {
		return fmt.Errorf("error deleting conntrack entries for %s port: %d, error: %w", protoStr(protocol), port, err)
	}
	klog.V(4).InfoS("Cleared conntrack entries", "count", n)
	return nil
}
