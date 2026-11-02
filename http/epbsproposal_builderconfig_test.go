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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	eth2http "github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/mock"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func validEPBSBuilderConfig() *gloas.BuilderConfig {
	return &gloas.BuilderConfig{
		Builders: []*gloas.BuilderEntry{{
			URL: []byte("https://builder.example"),
			Auth: &gloas.SignedBuilderRequestAuth{
				Message:   &gloas.BuilderRequestAuth{Data: []byte{0x01}, Slot: 123},
				Signature: phase0.BLSSignature{0x02},
			},
			BuilderPubkeys: []phase0.BLSPubKey{},
		}},
	}
}

func TestEPBSProposalJSONRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
			require.NoError(t, err)
		case "/eth/v1/node/version":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"version":"test"}}`))
			require.NoError(t, err)
		case "/eth/v4/validator/blocks/123":
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			require.Equal(t, "application/json", r.Header.Get("Accept"))
			w.WriteHeader(http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service, err := eth2http.New(context.Background(),
		eth2http.WithAddress(server.URL),
		eth2http.WithAllowDelayedStart(true),
		eth2http.WithEnforceJSON(true),
		eth2http.WithLogLevel(zerolog.Disabled),
	)
	require.NoError(t, err)

	includePayload := true
	_, err = service.(client.EPBSProposalProvider).EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot:           123,
		BuilderConfig:  validEPBSBuilderConfig(),
		IncludePayload: &includePayload,
	})
	require.Error(t, err)
}

func TestEPBSProposalJSONResponseAcceptsBareBuilderWin(t *testing.T) {
	mockService, err := mock.New(context.Background())
	require.NoError(t, err)
	excludePayload := false
	mockResponse, err := mockService.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot:           123,
		IncludePayload: &excludePayload,
	})
	require.NoError(t, err)
	block, err := json.Marshal(mockResponse.Data.Gloas)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
			require.NoError(t, err)
		case "/eth/v1/node/version":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"version":"test"}}`))
			require.NoError(t, err)
		case "/eth/v4/validator/blocks/123":
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			require.Equal(t, "application/json", r.Header.Get("Accept"))
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Eth-Consensus-Version", "gloas")
			_, err := w.Write(append([]byte(`{"version":"gloas","execution_payload_included":false,"data":`), append(block, '}')...))
			require.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service, err := eth2http.New(context.Background(),
		eth2http.WithAddress(server.URL),
		eth2http.WithAllowDelayedStart(true),
		eth2http.WithEnforceJSON(true),
		eth2http.WithLogLevel(zerolog.Disabled),
	)
	require.NoError(t, err)

	includePayload := true
	response, err := service.(client.EPBSProposalProvider).EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot:           123,
		BuilderConfig:  validEPBSBuilderConfig(),
		IncludePayload: &includePayload,
	})
	require.NoError(t, err)
	require.False(t, response.Data.ExecutionPayloadIncluded)
	require.NotNil(t, response.Data.Gloas)
	require.Nil(t, response.Data.GloasContents)
}

func TestEPBSProposalRejectsNonUTF8BuilderURL(t *testing.T) {
	var blockRequests atomic.Uint64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
			require.NoError(t, err)
		case "/eth/v1/node/version":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"version":"test"}}`))
			require.NoError(t, err)
		case "/eth/v4/validator/blocks/123":
			blockRequests.Add(1)
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service, err := eth2http.New(context.Background(),
		eth2http.WithAddress(server.URL),
		eth2http.WithAllowDelayedStart(true),
		eth2http.WithLogLevel(zerolog.Disabled),
	)
	require.NoError(t, err)

	includePayload := true
	config := validEPBSBuilderConfig()
	config.Builders[0].URL = []byte{0xff}

	_, err = service.(client.EPBSProposalProvider).EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot:           123,
		BuilderConfig:  config,
		IncludePayload: &includePayload,
	})
	require.ErrorContains(t, err, "builder 0 has invalid URL")
	require.Zero(t, blockRequests.Load())
}

func TestEPBSProposalBidPolicy(t *testing.T) {
	entry := func(url string, paymentCap, floor phase0.Gwei) *gloas.BuilderEntry {
		builder := validEPBSBuilderConfig().Builders[0]
		builder.URL = []byte(url)
		builder.MaxExecutionPayment = paymentCap
		builder.MinBid = floor
		return builder
	}
	tests := []struct {
		name                   string
		url                    []string
		config                 *gloas.BuilderConfig
		value, payment         phase0.Gwei
		self, reject, withheld bool
	}{
		{name: "unmatched below floor", url: []string{"https://unknown.example"}, config: &gloas.BuilderConfig{MinBid: 10}, value: 1, reject: true},
		{name: "duplicate below floor", url: []string{"https://a.example"}, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 2, 0), entry("https://a.example", 3, 0)}}, value: 1, reject: true},
		{name: "joined below floor", url: []string{"https://a.example", "https://b.example"}, config: &gloas.BuilderConfig{MinBid: 10}, value: 1, reject: true},
		{name: "joined header cannot match a single entry", url: []string{"https://a.example", "https://b.example"}, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example;https://b.example", 0, 0)}}, value: 1, reject: true},
		{name: "p2p payment below floor", config: &gloas.BuilderConfig{MinBid: 10}, value: 1, payment: 1, reject: true},
		{name: "p2p uses largest cap", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 1, 0), entry("https://b.example", 3, 0)}}, value: 7, payment: 4, withheld: true},
		{name: "p2p cap enforced", config: &gloas.BuilderConfig{MinBid: 11, Builders: []*gloas.BuilderEntry{entry("https://a.example", 1, 0), entry("https://b.example", 3, 0)}}, value: 7, payment: 4, reject: true},
		{name: "direct entry floor and cap", url: []string{"https://b.example"}, config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{entry("https://a.example", 1, 0), entry("https://b.example", 3, 10)}}, value: 7, payment: 3},
		{name: "direct entry cap enforced", url: []string{"https://a.example"}, config: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{entry("https://a.example", 1, 10), entry("https://b.example", 9, 0)}}, value: 7, payment: 3, reject: true},
		{name: "unmatched accepted but value withheld", url: []string{"https://unknown.example"}, config: &gloas.BuilderConfig{MinBid: 7}, value: 7, withheld: true},
		{name: "duplicate accepted but value withheld", url: []string{"https://a.example"}, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 1, 0), entry("https://a.example", 3, 0)}}, value: 7, payment: 3, withheld: true},
		{name: "self build retains value", config: &gloas.BuilderConfig{MinBid: 100}, self: true},
	}
	for _, test := range tests {
		for _, jsonResponse := range []bool{true, false} {
			name := test.name + "/ssz"
			if jsonResponse {
				name = test.name + "/json"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				mockService, err := mock.New(ctx)
				require.NoError(t, err)
				include := false
				mockResponse, err := mockService.EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include})
				require.NoError(t, err)
				bid := mockResponse.Data.Gloas.Body.SignedExecutionPayloadBid.Message
				bid.BuilderIndex = 1
				if test.self {
					bid.BuilderIndex = gloas.BuilderIndex(^uint64(0))
				}
				bid.Value, bid.ExecutionPayment = test.value, test.payment
				var block []byte
				if jsonResponse {
					block, err = json.Marshal(mockResponse.Data.Gloas)
				} else {
					block, err = mockResponse.Data.Gloas.MarshalSSZ()
				}
				require.NoError(t, err)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/eth/v1/node/syncing":
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
					case "/eth/v1/node/version":
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
					case "/eth/v4/validator/blocks/123":
						w.Header().Set("Eth-Consensus-Version", "gloas")
						w.Header().Set("Eth-Execution-Payload-Value", "7000000000")
						w.Header().Set("Eth-Consensus-Block-Value", "5")
						for _, url := range test.url {
							w.Header().Add("Eth-Builder-Url", url)
						}
						if jsonResponse {
							w.Header().Set("Content-Type", "application/json")
							_, _ = w.Write(append([]byte(`{"version":"gloas","execution_payload_included":false,"execution_payload_value":"7000000000","data":`), append(block, '}')...))
						} else {
							w.Header().Set("Content-Type", "application/octet-stream")
							w.Header().Set("Eth-Execution-Payload-Included", "false")
							_, _ = w.Write(block)
						}
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(jsonResponse), eth2http.WithLogLevel(zerolog.Disabled))
				require.NoError(t, err)
				response, err := service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, BuilderConfig: test.config, IncludePayload: &include})
				if test.reject {
					require.ErrorIs(t, err, client.ErrInconsistentResult)
					require.ErrorContains(t, err, "below minimum")
					return
				}
				require.NoError(t, err)
				if test.withheld {
					require.Nil(t, response.Data.ExecutionValue)
					require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
					require.NotContains(t, response.Metadata, "execution_payload_value")
					require.Equal(t, "5", response.Data.Value().String())
				} else {
					require.Equal(t, "7000000000", response.Data.ExecutionValue.String())
					require.Equal(t, "7000000005", response.Data.Value().String())
					require.Contains(t, response.Metadata, "Eth-Execution-Payload-Value")
				}
			})
		}
	}
}
