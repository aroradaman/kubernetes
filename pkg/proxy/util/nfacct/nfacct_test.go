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

package nfacct

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// fakeHandler is a mock implementation of the handler interface, designed for testing.
type fakeHandler struct {
	// requests stores instances of fakeRequest, capturing new requests.
	requests []*fakeRequest
	// responses holds responses for the subsequent fakeRequest.Execute calls.
	responses [][][]byte
	// errs holds errors for the subsequent fakeRequest.Execute calls.
	errs []error
}

// newRequest creates a request object with the given cmd, flags, predefined response and error.
// It additionally records the created request object.
func (fh *fakeHandler) newRequest(cmd int, flags uint16) request {
	var response [][]byte
	if fh.responses != nil && len(fh.responses) > 0 {
		response = fh.responses[0]
		// remove the response from the list of predefined responses and add it to request object for mocking.
		fh.responses = fh.responses[1:]
	}

	var err error
	if fh.errs != nil && len(fh.errs) > 0 {
		err = fh.errs[0]
		// remove the error from the list of predefined errors and add it to request object for mocking.
		fh.errs = fh.errs[1:]
	}

	req := &fakeRequest{cmd: cmd, flags: flags, response: response, err: err}
	fh.requests = append(fh.requests, req)
	return req
}

// fakeRequest records information about the cmd and flags used when creating a new request,
// maintains a list for netlink attributes, and stores a predefined response and an optional
// error for subsequent execution.
type fakeRequest struct {
	// cmd and flags which were used to create the request.
	cmd   int
	flags uint16

	// data holds netlink attributes.
	data []nl.NetlinkRequestData

	// response and err are the predefined output of execution.
	response [][]byte
	err      error
}

// Serialize is part of request interface.
func (fr *fakeRequest) Serialize() []byte { return nil }

// AddData is part of request interface.
func (fr *fakeRequest) AddData(data nl.NetlinkRequestData) {
	fr.data = append(fr.data, data)
}

// AddRawData is part of request interface.
func (fr *fakeRequest) AddRawData(_ []byte) {}

// Execute is part of request interface.
func (fr *fakeRequest) Execute(_ int, _ uint16) ([][]byte, error) {
	return fr.response, fr.err
}

func TestRunner_Add(t *testing.T) {
	testCases := []struct {
		name         string
		counterName  string
		handler      *fakeHandler
		err          error
		netlinkCalls int
	}{
		{
			name:        "valid",
			counterName: "metric-1",
			handler:     &fakeHandler{},
			// expected calls: NFNL_MSG_ACCT_NEW
			netlinkCalls: 1,
		},
		{
			name:        "add duplicate counter",
			counterName: "metric-2",
			handler: &fakeHandler{
				errs: []error{syscall.EBUSY},
			},
			err: ErrObjectAlreadyExists,
			// expected calls: NFNL_MSG_ACCT_NEW
			netlinkCalls: 1,
		},
		{
			name:        "insufficient privilege",
			counterName: "metric-2",
			handler: &fakeHandler{
				errs: []error{syscall.EPERM},
			},
			err: ErrUnexpected,
			// expected calls: NFNL_MSG_ACCT_NEW
			netlinkCalls: 1,
		},
		{
			name:        "exceeds max length",
			counterName: "this-is-a-string-with-more-than-32-characters",
			handler:     &fakeHandler{},
			err:         ErrNameExceedsMaxLength,
			// expected calls: zero (the error should be returned by this library)
			netlinkCalls: 0,
		},
		{
			name:        "falls below min length",
			counterName: "",
			handler:     &fakeHandler{},
			err:         ErrEmptyName,
			// expected calls: zero (the error should be returned by this library)
			netlinkCalls: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rnr, err := newInternal(tc.handler)
			assert.NoError(t, err)

			err = rnr.Add(tc.counterName)
			if tc.err != nil {
				assert.ErrorContains(t, err, tc.err.Error())
			} else {
				assert.NoError(t, err)
			}

			// validate number of requests
			assert.Equal(t, tc.netlinkCalls, len(tc.handler.requests))

			if tc.netlinkCalls > 0 {
				// validate request
				assert.Equal(t, cmdNew, tc.handler.requests[0].cmd)
				assert.Equal(t, uint16(unix.NLM_F_REQUEST|unix.NLM_F_CREATE|unix.NLM_F_ACK), tc.handler.requests[0].flags)

				// validate attribute(NFACCT_NAME)
				assert.Equal(t, 1, len(tc.handler.requests[0].data))
				assert.Equal(t,
					tc.handler.requests[0].data[0].Serialize(),
					nl.NewRtAttr(attrName, nl.ZeroTerminated(tc.counterName)).Serialize(),
				)
			}
		})
	}
}
func TestRunner_Get(t *testing.T) {
	testCases := []struct {
		name         string
		counterName  string
		counter      *Counter
		handler      *fakeHandler
		netlinkCalls int
		err          error
	}{
		{
			name:        "valid with padding",
			counterName: "metric-1",
			counter:     &Counter{Name: "metric-1", Packets: 43214632547, Bytes: 2548697864523217},
			handler: &fakeHandler{
				responses: [][][]byte{{{0, 0, 0, 0, 13, 0, 1, 0, 109, 101, 116, 114, 105, 99, 45, 49, 0, 0, 0, 0, 12, 0, 2, 0, 0, 0, 0, 10, 15, 202, 246, 99, 12, 0, 3, 0, 0, 9, 14, 6, 246, 218, 205, 209, 8, 0, 4, 0, 0, 0, 0, 1}}},
			},
			// expected calls: NFNL_MSG_ACCT_GET
			netlinkCalls: 1,
		},
		{
			name:        "valid without padding",
			counterName: "metrics",
			counter:     &Counter{Name: "metrics", Packets: 12, Bytes: 503},
			handler: &fakeHandler{
				responses: [][][]byte{{{0, 0, 0, 0, 12, 0, 1, 0, 109, 101, 116, 114, 105, 99, 115, 0, 12, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 12, 12, 0, 3, 0, 0, 0, 0, 0, 0, 0, 1, 247, 8, 0, 4, 0, 0, 0, 0, 1}}},
			},
			// expected calls: NFNL_MSG_ACCT_GET
			netlinkCalls: 1,
		},
		{
			name:        "missing netfilter generic header",
			counterName: "metrics",
			counter:     nil,
			handler: &fakeHandler{
				responses: [][][]byte{{{1, 0, 109, 101, 116, 114, 105, 99, 115, 0, 12, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 12, 12, 0, 3, 0, 0, 0, 0, 0, 0, 0, 1, 247, 8, 0, 4, 0, 0, 0, 0, 1}}},
			},
			// expected calls: NFNL_MSG_ACCT_GET
			netlinkCalls: 1,
			err:          ErrUnexpected,
		},
		{
			name:        "incorrect padding",
			counterName: "metric-1",
			counter:     nil,
			handler: &fakeHandler{
				responses: [][][]byte{{{0, 0, 0, 0, 13, 0, 1, 0, 109, 101, 116, 114, 105, 99, 45, 49, 0, 12, 0, 2, 0, 0, 0, 0, 10, 15, 202, 246, 99, 12, 0, 3, 0, 0, 9, 14, 6, 246, 218, 205, 209, 8, 0, 4, 0, 0, 0, 0, 1}}},
			},
			// expected calls: NFNL_MSG_ACCT_GET
			netlinkCalls: 1,
			err:          ErrUnexpected,
		},
		{
			name:        "missing bytes attribute",
			counterName: "metric-1",
			counter:     nil,
			handler: &fakeHandler{
				responses: [][][]byte{{{0, 0, 0, 0, 13, 0, 1, 0, 109, 101, 116, 114, 105, 99, 45, 49, 0, 0, 0, 0, 12, 0, 2, 0, 0, 0, 0, 10, 15, 202, 246, 99, 8, 0, 4, 0, 0, 0, 0, 1}}},
			},
			// expected calls: NFNL_MSG_ACCT_GET
			netlinkCalls: 1,
			err:          ErrUnexpected,
		},
		{
			name:        "missing packets attribute",
			counterName: "metric-1",
			counter:     nil,
			handler: &fakeHandler{
				responses: [][][]byte{{{0, 0, 0, 0, 13, 0, 1, 0, 109, 101, 116, 114, 105, 99, 45, 49, 0, 0, 0, 0, 12, 0, 3, 0, 0, 9, 14, 6, 246, 218, 205, 209, 8, 0, 4, 0, 0, 0, 0, 1}}},
			},
			// expected calls: NFNL_MSG_ACCT_GET
			netlinkCalls: 1,
			err:          ErrUnexpected,
		},
		{
			name:        "only name attribute",
			counterName: "metric-1",
			counter:     nil,
			handler: &fakeHandler{
				responses: [][][]byte{{{0, 0, 0, 0, 13, 0, 1, 0, 109, 101, 116, 114, 105, 99, 45, 49, 0, 0, 0, 0}}},
			},
			// expected calls: NFNL_MSG_ACCT_GET
			netlinkCalls: 1,
			err:          ErrUnexpected,
		},
		{
			name:        "get non-existent counter",
			counterName: "metric-2",
			handler: &fakeHandler{
				errs: []error{syscall.ENOENT},
			},
			// expected calls: NFNL_MSG_ACCT_GET
			netlinkCalls: 1,
			err:          ErrObjectNotFound,
		},
		{
			name:        "unexpected error",
			counterName: "metric-2",
			handler: &fakeHandler{
				errs: []error{syscall.EMFILE},
			},
			// expected calls: NFNL_MSG_ACCT_GET
			netlinkCalls: 1,
			err:          ErrUnexpected,
		},
		{
			name:        "exceeds max length",
			counterName: "this-is-a-string-with-more-than-32-characters",
			handler:     &fakeHandler{},
			// expected calls: zero (the error should be returned by this library)
			netlinkCalls: 0,
			err:          ErrNameExceedsMaxLength,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rnr, err := newInternal(tc.handler)
			assert.NoError(t, err)

			counter, err := rnr.Get(tc.counterName)

			// validate number of requests
			assert.Equal(t, tc.netlinkCalls, len(tc.handler.requests))
			if tc.netlinkCalls > 0 {
				// validate request
				assert.Equal(t, cmdGet, tc.handler.requests[0].cmd)
				assert.Equal(t, uint16(unix.NLM_F_REQUEST|unix.NLM_F_ACK), tc.handler.requests[0].flags)

				// validate attribute(NFACCT_NAME)
				assert.Equal(t, 1, len(tc.handler.requests[0].data))
				assert.Equal(t,
					tc.handler.requests[0].data[0].Serialize(),
					nl.NewRtAttr(attrName, nl.ZeroTerminated(tc.counterName)).Serialize())

				// validate response
				if tc.err != nil {
					assert.Nil(t, counter)
					assert.ErrorContains(t, err, tc.err.Error())
				} else {
					assert.NotNil(t, counter)
					assert.NoError(t, err)
					assert.Equal(t, tc.counter.Name, counter.Name)
					assert.Equal(t, tc.counter.Packets, counter.Packets)
					assert.Equal(t, tc.counter.Bytes, counter.Bytes)
				}
			}
		})
	}
}

func TestRunner_Ensure(t *testing.T) {
	testCases := []struct {
		name         string
		counterName  string
		netlinkCalls int
		handler      *fakeHandler
	}{
		{
			name:        "counter doesnt exist",
			counterName: "ct_established_accepted_packets",
			handler: &fakeHandler{
				errs: []error{syscall.ENOENT},
			},
			// expected calls - NFNL_MSG_ACCT_GET + NFNL_MSG_ACCT_NEW
			netlinkCalls: 2,
		},
		{
			name:        "counter already exists",
			counterName: "ct_invalid_dropped_packets",
			handler: &fakeHandler{
				responses: [][][]byte{{{0, 0, 0, 0, 31, 0, 1, 0, 99, 116, 95, 105, 110, 118, 97, 108, 105, 100, 95, 100, 114, 111, 112, 112, 101, 100, 95, 112, 97, 99, 107, 101, 116, 115, 0, 0, 12, 0, 2, 0, 0, 2, 104, 243, 22, 88, 14, 99, 12, 0, 3, 0, 18, 197, 55, 223, 229, 161, 205, 209, 8, 0, 4, 0, 0, 0, 0, 1}}},
			},
			// expected calls - NFNL_MSG_ACCT_GET
			netlinkCalls: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rnr, err := newInternal(tc.handler)
			assert.NoError(t, err)

			err = rnr.Ensure(tc.counterName)
			assert.NoError(t, err)

			// validate number of netlink requests
			assert.Equal(t, tc.netlinkCalls, len(tc.handler.requests))
		})
	}

}

func TestRunner_List(t *testing.T) {
	hndlr := &fakeHandler{
		responses: [][][]byte{{
			{0, 0, 0, 0, 23, 0, 1, 0, 114, 97, 110, 100, 111, 109, 45, 116, 101, 115, 116, 45, 109, 101, 116, 114, 105, 99, 0, 0, 12, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 134, 12, 0, 3, 0, 0, 0, 0, 0, 0, 0, 8, 96, 8, 0, 4, 0, 0, 0, 0, 1},
			{0, 0, 0, 0, 28, 0, 1, 0, 110, 102, 97, 99, 99, 116, 45, 108, 105, 115, 116, 45, 116, 101, 115, 116, 45, 109, 101, 116, 114, 105, 99, 0, 12, 0, 2, 0, 0, 0, 0, 0, 0, 2, 11, 150, 12, 0, 3, 0, 0, 0, 0, 0, 1, 230, 197, 116, 8, 0, 4, 0, 0, 0, 0, 1},
			{0, 0, 0, 0, 9, 0, 1, 0, 116, 101, 115, 116, 0, 0, 0, 0, 12, 0, 2, 0, 0, 0, 134, 141, 68, 235, 199, 2, 12, 0, 3, 0, 0, 110, 95, 226, 137, 105, 63, 158, 8, 0, 4, 0, 0, 0, 0, 1},
			{0, 0, 0, 0, 31, 0, 1, 0, 99, 116, 95, 105, 110, 118, 97, 108, 105, 100, 95, 100, 114, 111, 112, 112, 101, 100, 95, 112, 97, 99, 107, 101, 116, 115, 0, 0, 12, 0, 2, 0, 0, 0, 1, 30, 110, 172, 32, 233, 12, 0, 3, 0, 0, 0, 13, 109, 48, 17, 138, 236, 8, 0, 4, 0, 0, 0, 0, 1},
		}},
	}

	expected := []*Counter{
		{Name: "random-test-metric", Packets: 134, Bytes: 2144},
		{Name: "nfacct-list-test-metric", Packets: 134038, Bytes: 31901044},
		{Name: "test", Packets: 147941304813314, Bytes: 31067674010795934},
		{Name: "ct_invalid_dropped_packets", Packets: 1230217421033, Bytes: 14762609052396},
	}

	rnr, err := newInternal(hndlr)
	assert.NoError(t, err)

	counters, err := rnr.List()

	// validate request(NFNL_MSG_ACCT_GET)
	assert.Equal(t, 1, len(hndlr.requests))
	assert.Equal(t, cmdGet, hndlr.requests[0].cmd)
	assert.Equal(t, uint16(unix.NLM_F_REQUEST|unix.NLM_F_DUMP), hndlr.requests[0].flags)

	// validate attributes
	assert.Equal(t, 0, len(hndlr.requests[0].data))

	// validate response
	assert.NoError(t, err)
	assert.NotNil(t, counters)
	assert.Equal(t, len(expected), len(counters))
	for i := 0; i < len(expected); i++ {
		assert.Equal(t, expected[i].Name, counters[i].Name)
		assert.Equal(t, expected[i].Packets, counters[i].Packets)
		assert.Equal(t, expected[i].Bytes, counters[i].Bytes)
	}
}

func TestDecode(t *testing.T) {
	testCases := []struct {
		name     string
		msg      []byte
		expected *Counter
	}{
		{
			name:     "valid",
			msg:      []byte{0, 0, 0, 0, 13, 0, 1, 0, 109, 101, 116, 114, 105, 99, 45, 49, 0, 0, 0, 0, 12, 0, 2, 0, 0, 0, 0, 10, 15, 202, 246, 99, 12, 0, 3, 0, 0, 9, 14, 6, 246, 218, 205, 209, 8, 0, 4, 0, 0, 0, 0, 1},
			expected: &Counter{Name: "metric-1", Packets: 43214632547, Bytes: 2548697864523217},
		},
		{
			name:     "attribute name missing",
			msg:      []byte{0, 0, 0, 0, 12, 0, 2, 0, 0, 0, 0, 0, 0, 2, 11, 150, 12, 0, 3, 0, 0, 0, 0, 0, 1, 230, 197, 116, 8, 0, 4, 0, 0, 0, 0, 1},
			expected: &Counter{Packets: 134038, Bytes: 31901044},
		},
		{
			name:     "attribute packets missing",
			msg:      []byte{0, 0, 0, 0, 31, 0, 1, 0, 99, 116, 95, 105, 110, 118, 97, 108, 105, 100, 95, 100, 114, 111, 112, 112, 101, 100, 95, 112, 97, 99, 107, 101, 116, 115, 0, 0, 12, 0, 3, 0, 0, 0, 0, 0, 0, 0, 8, 96, 8, 0, 4, 0, 0, 0, 0, 1},
			expected: &Counter{Name: "ct_invalid_dropped_packets", Bytes: 2144},
		},
		{
			name:     "attribute bytes missing",
			msg:      []byte{0, 0, 0, 0, 23, 0, 1, 0, 114, 97, 110, 100, 111, 109, 45, 116, 101, 115, 116, 45, 109, 101, 116, 114, 105, 99, 0, 0, 12, 0, 2, 0, 0, 0, 0, 0, 0, 0, 1, 247},
			expected: &Counter{Name: "random-test-metric", Packets: 503},
		},
		{
			name:     "attribute packets and bytes missing",
			msg:      []byte{0, 0, 0, 0, 9, 0, 1, 0, 116, 101, 115, 116, 0, 0, 0, 0},
			expected: &Counter{Name: "test"},
		},
		{
			name:     "only netfilter generic header present",
			msg:      []byte{0, 0, 0, 0},
			expected: &Counter{},
		},
		{
			name:     "only packets attribute",
			msg:      []byte{0, 0, 0, 0, 12, 0, 2, 0, 0, 0, 0, 0, 0, 2, 11, 150},
			expected: &Counter{Packets: 134038},
		},
		{
			name:     "only bytes attribute",
			msg:      []byte{0, 0, 0, 0, 12, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 12},
			expected: &Counter{Bytes: 12},
		},
		{
			name:     "new attribute in the beginning",
			msg:      []byte{0, 0, 0, 0, 8, 0, 0, 0, 0, 0, 0, 1, 13, 0, 1, 0, 109, 101, 116, 114, 105, 99, 45, 49, 0, 0, 0, 0, 12, 0, 2, 0, 0, 0, 0, 10, 15, 202, 246, 99, 12, 0, 3, 0, 0, 9, 14, 6, 246, 218, 205, 209, 8, 0, 4, 0, 0, 0, 0, 1},
			expected: &Counter{Name: "metric-1", Packets: 43214632547, Bytes: 2548697864523217},
		},
		{
			name:     "new attribute in the end",
			msg:      []byte{0, 0, 0, 0, 8, 0, 0, 0, 0, 0, 0, 1, 13, 0, 1, 0, 109, 101, 116, 114, 105, 99, 45, 49, 0, 0, 0, 0, 12, 0, 2, 0, 0, 0, 0, 10, 15, 202, 246, 99, 12, 0, 3, 0, 0, 9, 14, 6, 246, 218, 205, 209, 8, 0, 4, 0, 0, 0, 0, 1, 12, 0, 0, 1, 2, 3, 14, 63, 246, 218, 205, 209},
			expected: &Counter{Name: "metric-1", Packets: 43214632547, Bytes: 2548697864523217},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			counter, err := decode(tc.msg, false)
			assert.NoError(t, err)
			assert.NotNil(t, counter)

			assert.Equal(t, tc.expected.Name, counter.Name)
			assert.Equal(t, tc.expected.Packets, counter.Packets)
			assert.Equal(t, tc.expected.Bytes, counter.Bytes)

		})
	}
}
