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

package http_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	eth2http "github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestPayloadAttestationPoolRejectsOversizedContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
		case "/eth/v1/beacon/pool/payload_attestations":
			w.Header().Set("Content-Length", "67108865")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL))
	require.NoError(t, err)
	_, err = service.(client.PayloadAttestationPoolProvider).PayloadAttestationPool(ctx, &api.PayloadAttestationPoolOpts{})
	require.ErrorContains(t, err, "response body exceeds 67108864 bytes")
}

type responseTransport struct {
	base        http.RoundTripper
	contentType string
	path        string
	length      int64
	reads       int
	accept      string
}

func (r *responseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path != r.path {
		return r.base.RoundTrip(req)
	}
	r.accept = req.Header.Get("Accept")
	header := make(http.Header)
	if r.contentType != "" {
		header.Set("Content-Type", r.contentType)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: r, ContentLength: r.length}, nil
}

func (r *responseTransport) Read(_ []byte) (int, error) {
	r.reads++
	return 0, errors.New("body was read")
}

func (*responseTransport) Close() error { return nil }

func TestEPBSProposalUsesResponseEncodingLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tests := []struct {
		name        string
		contentType string
		length      int64
		enforceJSON bool
		wantRead    bool
		limit       string
	}{
		{name: "JSONAfterSSZ", contentType: "application/json", length: 268435457, wantRead: true},
		{name: "MissingContentType", length: 268435457, wantRead: true},
		{name: "UnknownContentType", contentType: "text/plain", length: 268435457, wantRead: true},
		{name: "SSZAfterJSON", contentType: "application/octet-stream", length: 268435457, enforceJSON: true, limit: "268435456"},
		{name: "JSONAboveLimit", contentType: "application/json", length: 536870913, limit: "536870912"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &responseTransport{base: http.DefaultTransport, path: "/eth/v4/validator/blocks/123", contentType: test.contentType, length: test.length}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithHTTPClient(&http.Client{Transport: transport}), eth2http.WithEnforceJSON(test.enforceJSON))
			require.NoError(t, err)
			include := false
			_, err = service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
				Slot: 123, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}},
			})
			if test.enforceJSON {
				require.Equal(t, "application/json", transport.accept)
			} else {
				require.Equal(t, "application/octet-stream", transport.accept)
			}
			if test.wantRead {
				require.ErrorContains(t, err, "body was read")
				require.Equal(t, 1, transport.reads)
			} else {
				require.ErrorContains(t, err, "response body exceeds "+test.limit+" bytes")
				require.Zero(t, transport.reads)
			}
		})
	}
}

func TestEPBSReadEndpointsRejectOversizedContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	root := phase0.Root{1}
	tests := []struct {
		name string
		path string
		call func(context.Context, client.Service) error
	}{
		{name: "Envelope", path: fmt.Sprintf("/eth/v1/validator/execution_payload_envelopes/123/%#x", root), call: func(ctx context.Context, s client.Service) error {
			_, err := s.(client.ExecutionPayloadEnvelopeProvider).ExecutionPayloadEnvelope(ctx, &api.ExecutionPayloadEnvelopeOpts{Slot: 123, BeaconBlockRoot: root})
			return err
		}},
		{name: "SignedEnvelope", path: "/eth/v1/beacon/execution_payload_envelopes/head", call: func(ctx context.Context, s client.Service) error {
			_, err := s.(client.ExecutionPayloadProvider).SignedExecutionPayloadEnvelope(ctx, &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{name: "AttestationData", path: "/eth/v1/validator/payload_attestation_data", call: func(ctx context.Context, s client.Service) error {
			_, err := s.(client.PayloadAttestationDataProvider).PayloadAttestationData(ctx, &api.PayloadAttestationDataOpts{Slot: 123})
			return err
		}},
		{name: "AttestationPool", path: "/eth/v1/beacon/pool/payload_attestations", call: func(ctx context.Context, s client.Service) error {
			_, err := s.(client.PayloadAttestationPoolProvider).PayloadAttestationPool(ctx, &api.PayloadAttestationPoolOpts{})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &responseTransport{base: http.DefaultTransport, path: test.path, contentType: "application/json", length: 67108865}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			err = test.call(ctx, service)
			require.ErrorContains(t, err, "response body exceeds 67108864 bytes")
			require.Zero(t, transport.reads)
		})
	}
}
