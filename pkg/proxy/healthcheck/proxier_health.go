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

package healthcheck

import (
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"
)

// ProxierHealthManager allows callers to update healthz timestamp only.
type ProxierHealthManager interface {
	// QueuedUpdate should be called when the proxier receives a Service or Endpoints
	// event containing information that requires updating service rules.
	QueuedUpdate()

	// Updated should be called when the proxier has successfully updated the service
	// rules to reflect the current state.
	Updated()

	proxierHealthChecker
}

var _ ProxierHealthManager = &proxierHealthManager{}
var zeroTime = time.Time{}

// proxierHealthManager verifies that the delay between
// QueuedUpdate() calls and Updated() calls never exceeds healthTimeout.
type proxierHealthManager struct {
	clock         clock.Clock
	healthTimeout time.Duration

	lastUpdated         atomic.Value
	oldestPendingQueued atomic.Value
}

func newProxierHealthManager(c clock.Clock, healthTimeout time.Duration) *proxierHealthManager {
	return &proxierHealthManager{
		clock:         c,
		healthTimeout: healthTimeout,
	}
}

// Updated indicates that kube-proxy has successfully updated its backend, so it should
// be considered healthy now.
func (hs *proxierHealthManager) Updated() {
	hs.oldestPendingQueued.Store(zeroTime)
	hs.lastUpdated.Store(hs.clock.Now())
}

// QueuedUpdate indicates that the proxy has received changes from the apiserver but
// has not yet pushed them to its backend. If the proxy does not call Updated within the
// healthTimeout time then it will be considered unhealthy.
func (hs *proxierHealthManager) QueuedUpdate() {
	// Set oldestPendingQueued only if it's currently zero
	hs.oldestPendingQueued.CompareAndSwap(zeroTime, hs.clock.Now())
}

// IsHealthy returns the proxier's health state, following the same definition
// the HTTP server defines.
func (hs *proxierHealthManager) IsHealthy() bool {
	isHealthy, _, _ := hs.isHealthy()
	return isHealthy
}

func (hs *proxierHealthManager) isHealthy() (bool, time.Time, time.Time) {
	var oldestPendingQueued, lastUpdated time.Time
	if val := hs.oldestPendingQueued.Load(); val != nil {
		oldestPendingQueued = val.(time.Time)
	}
	if val := hs.lastUpdated.Load(); val != nil {
		lastUpdated = val.(time.Time)
	}
	currentTime := hs.clock.Now()

	healthy := false
	switch {
	case oldestPendingQueued.IsZero():
		// The proxy is healthy while it's starting up
		// or the proxy is fully synced.
		healthy = true
	case currentTime.Sub(oldestPendingQueued) < hs.healthTimeout:
		// There's an unprocessed update queued, but it's not late yet
		healthy = true
	}

	return healthy, lastUpdated, currentTime
}
