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
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	eth2http "github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type limitTransport func(*http.Request) (*http.Response, error)

func (f limitTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type observedBody struct{ reads atomic.Int32 }

func (b *observedBody) Read(_ []byte) (int, error) {
	b.reads.Add(1)
	return 0, io.EOF
}
func (*observedBody) Close() error { return nil }

func TestEPBSResponseLimits(t *testing.T) {
	include := false
	tests := []struct {
		name, path, contentType, limit string
		length                         int64
		call                           func(context.Context, client.Service) error
	}{
		{name: "ProposalSSZ", path: "/eth/v4/validator/blocks/1", contentType: "application/octet-stream", length: 256*1024*1024 + 1, limit: "268435456", call: func(ctx context.Context, s client.Service) error {
			_, err := s.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 1, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}})
			return err
		}},
		{name: "ProposalJSON", path: "/eth/v4/validator/blocks/1", contentType: "application/json", length: 512*1024*1024 + 1, limit: "536870912", call: func(ctx context.Context, s client.Service) error {
			_, err := s.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 1, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}})
			return err
		}},
		{name: "CachedEnvelope", path: "/eth/v1/validator/execution_payload_envelopes/", contentType: "application/octet-stream", length: 64*1024*1024 + 1, limit: "67108864", call: func(ctx context.Context, s client.Service) error {
			_, err := s.(client.ExecutionPayloadEnvelopeProvider).ExecutionPayloadEnvelope(ctx, &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})
			return err
		}},
		{name: "SignedEnvelope", path: "/eth/v1/beacon/execution_payload_envelopes/", contentType: "application/json", length: 64*1024*1024 + 1, limit: "67108864", call: func(ctx context.Context, s client.Service) error {
			_, err := s.(client.ExecutionPayloadProvider).SignedExecutionPayloadEnvelope(ctx, &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{name: "AttestationData", path: "/eth/v1/validator/payload_attestation_data", contentType: "application/octet-stream", length: 64*1024*1024 + 1, limit: "67108864", call: func(ctx context.Context, s client.Service) error {
			_, err := s.(client.PayloadAttestationDataProvider).PayloadAttestationData(ctx, &api.PayloadAttestationDataOpts{Slot: 1})
			return err
		}},
		{name: "AttestationPool", path: "/eth/v1/beacon/pool/payload_attestations", contentType: "application/json", length: 64*1024*1024 + 1, limit: "67108864", call: func(ctx context.Context, s client.Service) error {
			_, err := s.(client.PayloadAttestationPoolProvider).PayloadAttestationPool(ctx, &api.PayloadAttestationPoolOpts{})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &observedBody{}
			var accept atomic.Value
			transport := limitTransport(func(req *http.Request) (*http.Response, error) {
				header := http.Header{"Content-Type": {"application/json"}}
				if strings.HasPrefix(req.URL.Path, test.path) {
					accept.Store(req.Header.Get("Accept"))
					header.Set("Content-Type", test.contentType)
					return &http.Response{StatusCode: http.StatusOK, Header: header, Body: body, ContentLength: test.length, Request: req}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(`{"data":{"version":"test","head_slot":"0","sync_distance":"0","is_syncing":false}}`)), Request: req}, nil
			})
			service, err := eth2http.New(ctx, eth2http.WithAddress("http://example.test"), eth2http.WithHTTPClient(&http.Client{Transport: transport}), eth2http.WithLogLevel(zerolog.Disabled))
			require.NoError(t, err)
			err = test.call(ctx, service)
			require.ErrorContains(t, err, "response body exceeds "+test.limit+" bytes")
			require.Zero(t, body.reads.Load())
			if test.name == "ProposalJSON" {
				require.Equal(t, "application/octet-stream", accept.Load())
			}
		})
	}
}
