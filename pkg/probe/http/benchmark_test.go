package http

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"net/http"
	_ "net/http/pprof"
	"testing"
	"time"
)

const NProbers = 500
const NProbes = 120

var empty = struct{}{}

var reusableReq *http.Request

func createRequestObject() *http.Request {
	req, _ := NewRequestForHTTPGetAction(&v1.HTTPGetAction{
		Path:        "/",
		Port:        intstr.IntOrString{IntVal: 5000},
		Host:        "",
		Scheme:      "http",
		HTTPHeaders: nil,
	}, &v1.Container{
		Name:    "test",
		Image:   "nginx",
		Command: []string{"nginx"},
		Args:    []string{"--restart=Never"},
	}, "127.0.0.1", "bench-marker")
	return req
}

func worker(waiter chan struct{}, reuse bool) {
	var req *http.Request
	prober := New(false)

	if reuse {
		prober.SetRequestObj(createRequestObject())
	}

	for i := 0; i < NProbes; i++ {
		if reuse {
			req = nil
		} else {
			req = createRequestObject()
		}
		_, _, _ = prober.Probe(req, time.Second)

		time.Sleep(time.Second)
	}
	waiter <- empty
}

func TestBench(t *testing.T) {
	waiter := make(chan struct{}, NProbers)

	for i := 0; i < NProbers; i++ {
		go worker(waiter, false)
	}

	for i := 0; i < NProbers; i++ {
		<-waiter
	}
}
