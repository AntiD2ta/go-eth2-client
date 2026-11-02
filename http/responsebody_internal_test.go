// Copyright © 2026 Attestant Limited.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package http

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"

	"github.com/stretchr/testify/require"
)

// testResponseLimit keeps the two encodings' limits apart so a test can tell
// which one bounded a body.
var testResponseLimit = responseLimit{ssz: 4, json: 8}

// limitTestService returns a service whose /test endpoint is answered by handler.
func limitTestService(ctx context.Context, t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		default:
			handler(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	svc, err := New(ctx, WithAddress(srv.URL))
	require.NoError(t, err)

	return svc.(*Service)
}

// respond returns a handler that answers with body under contentType, which is
// omitted when empty.  It streams the body without a Content-Length, so only
// the read can enforce the limit.
func respond(contentType string, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if contentType == "" {
			// Stop the server sniffing one.
			w.Header()["Content-Type"] = nil
		} else {
			w.Header().Set("Content-Type", contentType)
		}
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte(body))
	}
}

func TestResponseLimitFollowsResponseEncoding(t *testing.T) {
	tests := []struct {
		name        string
		limit       *responseLimit
		contentType string
		body        string
		err         string
	}{
		{
			name:        "JSONResponseToSSZRequest",
			contentType: "application/json",
			body:        `{"a":1}`,
		},
		{
			name:        "SSZResponseExceedsSSZLimit",
			contentType: "application/octet-stream",
			body:        "12345",
			err:         "response body exceeds 4 bytes",
		},
		{
			name:        "SSZResponseAtSSZLimit",
			contentType: "application/octet-stream",
			body:        "1234",
		},
		{
			name:        "JSONResponseAtJSONLimit",
			contentType: "application/json",
			body:        `{"a":12}`,
		},
		{
			name:        "JSONResponseExceedsJSONLimit",
			contentType: "application/json",
			body:        `{"a":123}`,
			err:         "response body exceeds 8 bytes",
		},
		{
			name: "NoContentTypeGetsJSONLimit",
			body: `{"a":12}`,
		},
		{
			name:        "UnknownContentTypeGetsJSONLimit",
			contentType: "text/plain",
			body:        `{"a":12}`,
		},
		{
			name:        "ZeroSSZLimitLeavesSSZUnbounded",
			limit:       &responseLimit{json: 8},
			contentType: "application/octet-stream",
			body:        "123456789",
		},
		{
			name:        "ZeroSSZLimitKeepsJSONBounded",
			limit:       &responseLimit{json: 8},
			contentType: "application/json",
			body:        `{"a":123}`,
			err:         "response body exceeds 8 bytes",
		},
		{
			name:        "ZeroJSONLimitLeavesJSONUnbounded",
			limit:       &responseLimit{ssz: 4},
			contentType: "application/json",
			body:        `{"a":123}`,
		},
	}

	// Both methods ask for SSZ, so a JSON answer is always the node's choice.
	methods := map[string]func(ctx context.Context, s *Service, limit responseLimit) (*httpResponse, error){
		"GET": func(ctx context.Context, s *Service, limit responseLimit) (*httpResponse, error) {
			return s.getWithResponseLimit(ctx, "/test", "", &api.CommonOpts{}, true, limit)
		},
		"POST": func(ctx context.Context, s *Service, limit responseLimit) (*httpResponse, error) {
			return s.postWithResponseLimit(ctx, "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ,
				map[string]string{"Accept": ContentTypeSSZ.MediaType()}, limit)
		},
	}

	for _, test := range tests {
		for method, call := range methods {
			t.Run(test.name+"/"+method, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				limit := testResponseLimit
				if test.limit != nil {
					limit = *test.limit
				}

				s := limitTestService(ctx, t, respond(test.contentType, test.body))
				res, err := call(ctx, s, limit)
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
				} else {
					require.NoError(t, err)
					require.Equal(t, []byte(test.body), res.body)
				}
			})
		}
	}
}

func TestDeclaredOversizedResponseIsRejectedUnread(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The handler declares a body over the JSON limit and never sends it, so
	// a client that reads the body times out rather than failing on the limit.
	s := limitTestService(ctx, t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "9")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})

	_, err := s.getWithResponseLimit(ctx, "/test", "", &api.CommonOpts{Timeout: 2 * time.Second}, true, testResponseLimit)
	require.ErrorContains(t, err, "declared response body of 9 bytes exceeds 8 bytes")
}

// TestEPBSResponseLimits pins each bounded endpoint's limits through its public
// method.  The node declares one byte over the limit, so nothing is allocated.
func TestEPBSResponseLimits(t *testing.T) {
	includePayload := true
	proposal := func(enforceJSON bool) func(ctx context.Context, s *Service) error {
		return func(ctx context.Context, s *Service) error {
			s.enforceJSON = enforceJSON
			_, err := s.EPBSProposal(ctx, &api.EPBSProposalOpts{
				Slot:           1,
				BuilderConfig:  &gloas.BuilderConfig{},
				IncludePayload: &includePayload,
			})

			return err
		}
	}

	endpoints := []struct {
		name string
		call func(ctx context.Context, s *Service) error
		ssz  int
		json int
	}{
		{
			name: "EPBSProposalSSZRequest",
			call: proposal(false),
			ssz:  256 * 1024 * 1024,
			json: 512 * 1024 * 1024,
		},
		{
			name: "EPBSProposalJSONRequest",
			call: proposal(true),
			ssz:  256 * 1024 * 1024,
			json: 512 * 1024 * 1024,
		},
		{
			name: "ExecutionPayloadEnvelope",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.ExecutionPayloadEnvelope(ctx, &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})

				return err
			},
			ssz:  64 * 1024 * 1024,
			json: 64 * 1024 * 1024,
		},
		{
			name: "SignedExecutionPayloadEnvelope",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.SignedExecutionPayloadEnvelope(ctx, &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})

				return err
			},
			ssz:  64 * 1024 * 1024,
			json: 64 * 1024 * 1024,
		},
		{
			name: "PayloadAttestationData",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.PayloadAttestationData(ctx, &api.PayloadAttestationDataOpts{Slot: 1})

				return err
			},
			ssz:  64 * 1024 * 1024,
			json: 64 * 1024 * 1024,
		},
		{
			name: "PayloadAttestationPool",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.PayloadAttestationPool(ctx, &api.PayloadAttestationPoolOpts{})

				return err
			},
			ssz:  64 * 1024 * 1024,
			json: 64 * 1024 * 1024,
		},
	}

	for _, endpoint := range endpoints {
		for contentType, limit := range map[string]int{
			"application/octet-stream": endpoint.ssz,
			"application/json":         endpoint.json,
		} {
			t.Run(endpoint.name+"/"+contentType, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				s := limitTestService(ctx, t, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", contentType)
					w.Header().Set("Content-Length", strconv.Itoa(limit+1))
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				})

				err := endpoint.call(ctx, s)
				require.ErrorContains(t, err, fmt.Sprintf("declared response body of %d bytes exceeds %d bytes", limit+1, limit))
			})
		}
	}
}

// TestEPBSProposalResponseLimitsAreSurvivable pins the property that makes the
// block-production limits a defence rather than a comment.  Unless the node
// declares the body's length, readResponseBody reaches its length check only
// after io.ReadAll has buffered the whole body, and ReadAll grows its buffer
// geometrically, so peak allocation is close to twice the limit.  A limit taken from the protocol's theoretical maximum
// (~2GiB of SSZ, ~5GiB of JSON hex) is therefore never reached -- the process
// dies first -- and a body admitted just under one is equally fatal.
//
// The ceiling below is an allocation budget, not a protocol bound: every live
// preset produces a payload-included response orders of magnitude smaller, so
// the JSON limit sitting exactly at half the budget is the deliberate ceiling
// rather than a coincidence.  Raising either limit past it is the regression
// this test exists to catch.
func TestEPBSProposalResponseLimitsAreSurvivable(t *testing.T) {
	const maxProposalResponsePeakAllocation = 1024 * 1024 * 1024

	tests := []struct {
		name  string
		limit int
	}{
		{name: "SSZ", limit: maxEPBSProposalResponseSize},
		{name: "JSON", limit: maxEPBSProposalJSONResponseSize},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.LessOrEqual(t, 2*test.limit, maxProposalResponsePeakAllocation)
		})
	}
}
