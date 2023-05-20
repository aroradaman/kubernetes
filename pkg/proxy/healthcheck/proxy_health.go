/*
Copyright 2023 The Kubernetes Authors.

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
	"fmt"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	api "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/utils/clock"
	"net/http"
	"time"
)

type ProxyHealthServer interface {
	// Run starts the healthz HTTP server and blocks until it exits.
	Run() error

	// NewProxierHealthManager returns a proxier health updater
	NewProxierHealthManager() ProxierHealthManager

	proxierHealthChecker
}

var _ ProxyHealthServer = &proxyHealthServer{}

// proxierHealthUpdater returns 200 "OK" by default. It verifies that the delay between
// QueuedUpdate() calls and Updated() calls never exceeds healthTimeout.
type proxyHealthServer struct {
	listener    listener
	httpFactory httpServerFactory
	clock       clock.Clock

	addr          string
	healthTimeout time.Duration
	recorder      events.EventRecorder
	nodeRef       *v1.ObjectReference

	proxierHealthManagers []ProxierHealthManager
}

// NewProxyHealthServer returns a proxier health http server.
func NewProxyHealthServer(addr string, healthTimeout time.Duration, recorder events.EventRecorder, nodeRef *v1.ObjectReference) ProxyHealthServer {
	return newProxyHealthServer(stdNetListener{}, stdHTTPServerFactory{}, clock.RealClock{}, addr, healthTimeout, recorder, nodeRef)
}

func newProxyHealthServer(listener listener, httpServerFactory httpServerFactory, c clock.Clock, addr string, healthTimeout time.Duration, recorder events.EventRecorder, nodeRef *v1.ObjectReference) *proxyHealthServer {
	return &proxyHealthServer{
		listener:              listener,
		httpFactory:           httpServerFactory,
		clock:                 c,
		addr:                  addr,
		healthTimeout:         healthTimeout,
		recorder:              recorder,
		nodeRef:               nodeRef,
		proxierHealthManagers: make([]ProxierHealthManager, 0),
	}
}

func (hs *proxyHealthServer) NewProxierHealthManager() ProxierHealthManager {
	phm := newProxierHealthManager(hs.clock, hs.healthTimeout)
	hs.proxierHealthManagers = append(hs.proxierHealthManagers, phm)
	return phm
}

// IsHealthy returns the proxier's health state, following the same definition
// the HTTP server defines.
func (hs *proxyHealthServer) IsHealthy() bool {
	isHealthy, _, _ := hs.isHealthy()
	return isHealthy
}

func (hs *proxyHealthServer) isHealthy() (bool, time.Time, time.Time) {
	healthy := true

	for _, phm := range hs.proxierHealthManagers {
		if !phm.IsHealthy() {
			healthy = false
		}
	}

	return healthy, time.Now(), time.Now()
}

// Run starts the healthz HTTP server and blocks until it exits.
func (hs *proxyHealthServer) Run() error {
	serveMux := http.NewServeMux()
	serveMux.Handle("/healthz", healthzHandler{hs: hs})
	server := hs.httpFactory.New(hs.addr, serveMux)

	listener, err := hs.listener.Listen(hs.addr)
	if err != nil {
		msg := fmt.Sprintf("failed to start proxier healthz on %s: %v", hs.addr, err)
		// TODO(thockin): move eventing back to caller
		if hs.recorder != nil {
			hs.recorder.Eventf(hs.nodeRef, nil, api.EventTypeWarning, "FailedToStartProxierHealthcheck", "StartKubeProxy", msg)
		}
		return fmt.Errorf("%v", msg)
	}

	klog.V(3).InfoS("Starting healthz HTTP server", "address", hs.addr)

	if err := server.Serve(listener); err != nil {
		return fmt.Errorf("proxier healthz closed with error: %v", err)
	}
	return nil
}

type healthzHandler struct {
	hs *proxyHealthServer
}

func (h healthzHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	healthy, lastUpdated, currentTime := h.hs.isHealthy()
	resp.Header().Set("Content-Type", "application/json")
	resp.Header().Set("X-Content-Type-Options", "nosniff")
	if !healthy {
		resp.WriteHeader(http.StatusServiceUnavailable)
	} else {
		resp.WriteHeader(http.StatusOK)
		// In older releases, the returned "lastUpdated" time indicated the last
		// time the proxier sync loop ran, even if nothing had changed. To
		// preserve compatibility, we use the same semantics: the returned
		// lastUpdated value is "recent" if the server is healthy. The kube-proxy
		// metrics provide more detailed information.
		lastUpdated = currentTime
	}
	fmt.Fprintf(resp, `{"lastUpdated": %q,"currentTime": %q}`, lastUpdated, currentTime)
}
