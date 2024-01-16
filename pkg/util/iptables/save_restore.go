//go:build linux
// +build linux

/*
Copyright 2014 The Kubernetes Authors.

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

package iptables

import (
	"bytes"
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/util/sets"
)

// MakeChainLine return an iptables-save/restore formatted chain line given a Chain
func MakeChainLine(chain Chain) string {
	return fmt.Sprintf(":%s - [0:0]", chain)
}

// GetChainsFromTable parses iptables-save data to find the chains that are defined. It
// assumes that save contains a single table's data, and returns a set with keys for every
// chain defined in that table.
func GetChainsFromTable(save []byte) sets.Set[Chain] {
	chainsSet := sets.New[Chain]()

	for {
		i := bytes.Index(save, []byte("\n:"))
		if i == -1 {
			break
		}
		start := i + 2
		save = save[start:]
		end := bytes.Index(save, []byte(" "))
		if end == -1 {
			// shouldn't happen, but...
			break
		}
		chain := Chain(save[:end])
		chainsSet.Insert(chain)
		save = save[end:]
	}
	return chainsSet
}

// GetCountersForRule parse and returns packet and byte counters associated with the rule.
func GetCountersForRule(save []byte, rule string) (int, int, error) {
	// \[(\d+):(\d+)] -A KUBE-FORWARD -m conntrack --ctstate INVALID -j DROP
	var err error
	index := bytes.Index(save, []byte(rule))
	pCounter, bCounter := 0, 0
	if index == -1 {
		return pCounter, bCounter, fmt.Errorf("rule not found")
	}

	start, end := index, index
	exit := false
	for start >= 0 && !exit {
		if start > 0 && save[start-1] == '-' && save[start] == 'A' {
			return 0, 0, fmt.Errorf("counter not preset in iptables-save output")
		}
		switch save[start] {
		case ']':
			end = start
		case ':':
			bCounter, err = strconv.Atoi(string(save[start+1 : end]))
			if err != nil {
				return 0, 0, err
			}
			end = start
		case '[':
			pCounter, err = strconv.Atoi(string(save[start+1 : end]))
			if err != nil {
				return 0, 0, err
			}
			exit = true
		}
		start--
	}
	return pCounter, bCounter, nil
}
