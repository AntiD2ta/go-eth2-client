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
	"fmt"
	"net/http"
	"net/http/httptest"
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

func requestBidProposal(t *testing.T, config *gloas.BuilderConfig, bid gloas.ExecutionPayloadBid, urls []string) (*api.Response[*api.VersionedEPBSProposal], error) {
	return requestBidProposalEncoding(t, config, bid, urls, true)
}

func requestBidProposalEncoding(t *testing.T, config *gloas.BuilderConfig, bid gloas.ExecutionPayloadBid, urls []string, jsonEncoding bool) (*api.Response[*api.VersionedEPBSProposal], error) {
	t.Helper()
	ctx := context.Background()
	include := false
	mockService, err := mock.New(ctx)
	require.NoError(t, err)
	mockResponse, err := mockService.EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include})
	require.NoError(t, err)
	bid.Slot = 123
	bid.BlobKZGCommitments = mockResponse.Data.Gloas.Body.SignedExecutionPayloadBid.Message.BlobKZGCommitments
	mockResponse.Data.Gloas.Body.SignedExecutionPayloadBid.Message = &bid
	var block []byte
	if jsonEncoding {
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
			if jsonEncoding {
				w.Header().Set("Content-Type", "application/json")
			} else {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Eth-Execution-Payload-Included", "false")
			}
			w.Header().Set("Eth-Consensus-Version", "gloas")
			w.Header().Set("Eth-Execution-Payload-Value", fmt.Sprint(uint64(1_000_000_000)))
			for _, url := range urls {
				w.Header().Add("Eth-Builder-Url", url)
			}
			if jsonEncoding {
				_, _ = fmt.Fprintf(w, `{"version":"gloas","execution_payload_included":false,"execution_payload_value":"%d","data":%s}`, uint64(1_000_000_000), block)
			} else {
				_, _ = w.Write(block)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(jsonEncoding), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	return service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, BuilderConfig: config, IncludePayload: &include})
}

func TestEPBSProposalUnmatchedBuilderStillChecksMinimum(t *testing.T) {
	_, err := requestBidProposal(t, &gloas.BuilderConfig{MinBid: 1_000_000_000}, gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1}, []string{"https://unknown.example"})
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "execution payload bid below minimum")
}

func TestEPBSProposalJoinedBuilderURLIsUnattributed(t *testing.T) {
	config := validEPBSBuilderConfig()
	config.MinBid = 10
	config.Builders[0].URL = []byte("https://first.example;https://second.example")
	_, err := requestBidProposal(t, config, gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1}, []string{"https://first.example", "https://second.example"})
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "execution payload bid below minimum")
}

func TestEPBSProposalSelfBuildKeepsExecutionValue(t *testing.T) {
	for _, jsonEncoding := range []bool{true, false} {
		t.Run(fmt.Sprintf("JSON=%t", jsonEncoding), func(t *testing.T) {
			response, err := requestBidProposalEncoding(t, &gloas.BuilderConfig{MinBid: 10}, gloas.ExecutionPayloadBid{BuilderIndex: gloas.BuilderIndex(^uint64(0))}, nil, jsonEncoding)
			require.NoError(t, err)
			require.Equal(t, fmt.Sprint(uint64(1_000_000_000)), response.Data.ExecutionValue.String())
			require.Equal(t, response.Data.ExecutionValue, response.Data.Value())
			require.Contains(t, response.Metadata, "Eth-Execution-Payload-Value")
		})
	}
}

func TestEPBSProposalWithheldValueRemovedFromMetadata(t *testing.T) {
	for _, jsonEncoding := range []bool{true, false} {
		t.Run(fmt.Sprintf("JSON=%t", jsonEncoding), func(t *testing.T) {
			response, err := requestBidProposalEncoding(t, &gloas.BuilderConfig{}, gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1}, []string{"https://unknown.example"}, jsonEncoding)
			require.NoError(t, err)
			require.Nil(t, response.Data.ExecutionValue)
			require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
			require.NotContains(t, response.Metadata, "execution_payload_value")
		})
	}
}

func TestEPBSProposalBidPolicy(t *testing.T) {
	entry := func(url string, floor, paymentCap uint64) *gloas.BuilderEntry {
		builder := validEPBSBuilderConfig().Builders[0]
		builder.URL = []byte(url)
		builder.MinBid = phase0.Gwei(floor)
		builder.MaxExecutionPayment = phase0.Gwei(paymentCap)
		return builder
	}
	tests := []struct {
		name    string
		config  *gloas.BuilderConfig
		bid     gloas.ExecutionPayloadBid
		urls    []string
		wantErr bool
		unknown bool
	}{
		{
			name: "DuplicateURLBelowFloor",
			config: &gloas.BuilderConfig{MinBid: 2, Builders: []*gloas.BuilderEntry{
				entry("https://builder.example", 0, 0), entry("https://builder.example", 0, 0),
			}},
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1}, urls: []string{"https://builder.example"}, wantErr: true,
		},
		{
			name: "DuplicateURLAcceptedWithValueWithheld",
			config: &gloas.BuilderConfig{MinBid: 2, Builders: []*gloas.BuilderEntry{
				entry("https://builder.example", 99, 1), entry("https://builder.example", 99, 0),
			}},
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1, ExecutionPayment: 1}, urls: []string{"https://builder.example"}, unknown: true,
		},
		{
			name:   "P2PPaymentBelowFloorWithoutEntries",
			config: &gloas.BuilderConfig{MinBid: 2}, bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1, ExecutionPayment: 1}, wantErr: true,
		},
		{
			name: "P2PUsesLargestCap",
			config: &gloas.BuilderConfig{MinBid: 5, Builders: []*gloas.BuilderEntry{
				entry("https://first.example", 0, 4), entry("https://second.example", 0, 2),
			}},
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1, ExecutionPayment: 7}, unknown: true,
		},
		{
			name: "UnmatchedURLUsesLargestCapButNotMore",
			config: &gloas.BuilderConfig{MinBid: 6, Builders: []*gloas.BuilderEntry{
				entry("https://first.example", 0, 4), entry("https://second.example", 0, 2),
			}},
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1, ExecutionPayment: 7}, urls: []string{"https://unknown.example"}, wantErr: true,
		},
		{
			name: "DirectUsesEntryFloorBelowP2PFloor",
			config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{
				entry("https://first.example", 100, 9), entry("https://builder.example", 2, 1),
			}},
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1, ExecutionPayment: 2}, urls: []string{"https://builder.example"},
		},
		{
			name: "DirectUsesResolvedEntryCap",
			config: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{
				entry("https://first.example", 0, 9), entry("https://builder.example", 3, 1),
			}},
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1, ExecutionPayment: 2}, urls: []string{"https://builder.example"}, wantErr: true,
		},
		{
			name:   "P2PWithoutPaymentKeepsVerifiedValue",
			config: &gloas.BuilderConfig{MinBid: 1}, bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1},
		},
		{
			name: "UnattributedTotalDoesNotOverflow",
			config: &gloas.BuilderConfig{MinBid: phase0.Gwei(^uint64(0)), Builders: []*gloas.BuilderEntry{
				entry("https://first.example", 0, 2),
			}},
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: phase0.Gwei(^uint64(0)), ExecutionPayment: 1}, unknown: true,
		},
	}
	for _, test := range tests {
		for _, jsonEncoding := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/JSON=%t", test.name, jsonEncoding), func(t *testing.T) {
				response, err := requestBidProposalEncoding(t, test.config, test.bid, test.urls, jsonEncoding)
				if test.wantErr {
					require.ErrorIs(t, err, client.ErrInconsistentResult)
					require.ErrorContains(t, err, "execution payload bid below minimum")
					return
				}
				require.NoError(t, err)
				if test.unknown {
					require.Nil(t, response.Data.ExecutionValue)
					require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
					require.NotContains(t, response.Metadata, "execution_payload_value")
				} else {
					require.Equal(t, fmt.Sprint(uint64(1_000_000_000)), response.Data.ExecutionValue.String())
					require.Contains(t, response.Metadata, "Eth-Execution-Payload-Value")
				}
			})
		}
	}
}
