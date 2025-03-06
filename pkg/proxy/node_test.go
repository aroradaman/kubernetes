/*
Copyright 2022 The Kubernetes Authors.

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

package proxy

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/test/utils/ktesting"
	netutils "k8s.io/utils/net"
)

func TestNodePodCIDRHandlerAdd(t *testing.T) {
	oldKlogOsExit := klog.OsExit
	defer func() {
		klog.OsExit = oldKlogOsExit
	}()
	klog.OsExit = customExit

	tests := []struct {
		name            string
		oldNodePodCIDRs []string
		newNodePodCIDRs []string
		expectPanic     bool
	}{
		{
			name: "both empty",
		},
		{
			name:            "initialized correctly",
			newNodePodCIDRs: []string{"192.168.1.0/24", "fd00:1:2:3::/64"},
		},
		{
			name:            "already initialized and same node",
			oldNodePodCIDRs: []string{"10.0.0.0/24", "fd00:3:2:1::/64"},
			newNodePodCIDRs: []string{"10.0.0.0/24", "fd00:3:2:1::/64"},
		},
		{
			name:            "already initialized and different node",
			oldNodePodCIDRs: []string{"192.168.1.0/24", "fd00:1:2:3::/64"},
			newNodePodCIDRs: []string{"10.0.0.0/24", "fd00:3:2:1::/64"},
			expectPanic:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &NodePodCIDRHandler{
				podCIDRs: tt.oldNodePodCIDRs,
			}
			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-node",
					ResourceVersion: "1",
				},
				Spec: v1.NodeSpec{
					PodCIDRs: tt.newNodePodCIDRs,
				},
			}
			defer func() {
				r := recover()
				if r == nil && tt.expectPanic {
					t.Errorf("The code did not panic")
				} else if r != nil && !tt.expectPanic {
					t.Errorf("The code did panic")
				}
			}()

			n.OnNodeAdd(node)
		})
	}
}

func TestNodePodCIDRHandlerUpdate(t *testing.T) {
	oldKlogOsExit := klog.OsExit
	defer func() {
		klog.OsExit = oldKlogOsExit
	}()
	klog.OsExit = customExit

	tests := []struct {
		name            string
		oldNodePodCIDRs []string
		newNodePodCIDRs []string
		expectPanic     bool
	}{
		{
			name: "both empty",
		},
		{
			name:            "initialize",
			newNodePodCIDRs: []string{"192.168.1.0/24", "fd00:1:2:3::/64"},
		},
		{
			name:            "same node",
			oldNodePodCIDRs: []string{"192.168.1.0/24", "fd00:1:2:3::/64"},
			newNodePodCIDRs: []string{"192.168.1.0/24", "fd00:1:2:3::/64"},
		},
		{
			name:            "different nodes",
			oldNodePodCIDRs: []string{"192.168.1.0/24", "fd00:1:2:3::/64"},
			newNodePodCIDRs: []string{"10.0.0.0/24", "fd00:3:2:1::/64"},
			expectPanic:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &NodePodCIDRHandler{
				podCIDRs: tt.oldNodePodCIDRs,
			}
			oldNode := &v1.Node{}
			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-node",
					ResourceVersion: "1",
				},
				Spec: v1.NodeSpec{
					PodCIDRs: tt.newNodePodCIDRs,
				},
			}
			defer func() {
				r := recover()
				if r == nil && tt.expectPanic {
					t.Errorf("The code did not panic")
				} else if r != nil && !tt.expectPanic {
					t.Errorf("The code did panic")
				}
			}()

			n.OnNodeUpdate(oldNode, node)
		})
	}
}

func TestNodeManagerUpsert(t *testing.T) {
	oldKlogOsExit := klog.OsExit
	defer func() {
		klog.OsExit = oldKlogOsExit
	}()
	klog.OsExit = customExit

	baseNode := &v1.Node{
		Status: v1.NodeStatus{
			Addresses: []v1.NodeAddress{
				{Type: v1.NodeInternalIP, Address: "192.168.1.1"},
				{Type: v1.NodeInternalIP, Address: "fd00:1:2:3::1"},
			},
		},
	}

	tests := []struct {
		name string
		// nodeIPs represent the initial NodeIPs fetched via getNodeIPs() in cmd/kube-proxy/server.go
		nodeIPs     []net.IP
		makeNode     func() *v1.Node
		expectPanic bool
	}{
		{
			name:    "no initial NodeIPs and node updated without NodeIPs",
			makeNode: func() *v1.Node { return &v1.Node{} },
		},
		{
			name:        "no initial NodeIPs and node updated with NodeIPs",
			makeNode:     func() *v1.Node { return baseNode.DeepCopy() },
			expectPanic: true,
		},
		{
			name:    "node updated with same NodeIPs",
			nodeIPs: []net.IP{netutils.ParseIPSloppy("192.168.1.1"), netutils.ParseIPSloppy("fd00:1:2:3::1")},
			makeNode: func() *v1.Node { return baseNode.DeepCopy() },
		},
		{
			name:    "node updated with different NodeIPs",
			nodeIPs: []net.IP{netutils.ParseIPSloppy("192.168.1.1"), netutils.ParseIPSloppy("fd00:1:2:3::1")},
			makeNode: func() *v1.Node {
				node := baseNode.DeepCopy()
				node.Status.Addresses = []v1.NodeAddress{
					{Type: v1.NodeInternalIP, Address: "10.0.1.1"},
					{Type: v1.NodeInternalIP, Address: "fd00:3:2:1::2"},
				}
				return node
			},
			expectPanic: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ctx := ktesting.NewTestContext(t)
			n := NewNodeManager(ctx, tc.nodeIPs)
			if tc.expectPanic {
				require.Panics(t, func() { n.OnNodeUpsert(tc.makeNode()) })
			} else {
				require.NotPanics(t, func() { n.OnNodeUpsert(tc.makeNode()) })
			}
		})
	}
}

func customExit(exitCode int) {
	panic(strconv.Itoa(exitCode))
}
