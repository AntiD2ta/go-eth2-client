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
	"math/big"
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

func TestEPBSProposalBidPolicy(t *testing.T) {
	entry := func(url string, floor, paymentCap phase0.Gwei) *gloas.BuilderEntry {
		builder := validEPBSBuilderConfig().Builders[0]
		builder.URL, builder.MinBid, builder.MaxExecutionPayment = []byte(url), floor, paymentCap
		return builder
	}
	tests := []struct {
		name, url, url2 string
		header          string
		builder         gloas.BuilderIndex
		value, payment  phase0.Gwei
		config          *gloas.BuilderConfig
		reject          bool
		execution       *big.Int
	}{
		{name: "UnmatchedBuilderBelowMinimum", url: "https://unknown.example", value: 1, config: &gloas.BuilderConfig{MinBid: 10}, reject: true},
		{name: "DuplicateBuilderBelowMinimum", url: "https://a.example", value: 1, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 0), entry("https://a.example", 0, 0)}}, reject: true},
		{name: "JoinedBuilderBelowMinimum", url: "https://a.example", url2: "https://b.example", value: 1, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example;https://b.example", 0, 0)}}, reject: true},
		{name: "P2PPaymentBelowMinimum", value: 1, payment: 1, config: &gloas.BuilderConfig{MinBid: 10}, reject: true},
		{name: "P2PUsesLargestCap", value: 4, payment: 9, config: &gloas.BuilderConfig{MinBid: 6, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://b.example", 0, 2)}}},
		{name: "P2PCapCannotExceedLargest", value: 4, payment: 9, config: &gloas.BuilderConfig{MinBid: 7, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://b.example", 0, 2)}}, reject: true},
		{name: "UnknownUsesLargestCap", url: "https://unknown.example", value: 4, payment: 9, config: &gloas.BuilderConfig{MinBid: 6, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://b.example", 0, 2)}}},
		{name: "AttributedUsesEntryFloor", url: "https://b.example", value: 5, header: "5000000000", config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{entry("https://a.example", 50, 99), entry("https://b.example", 5, 0)}}, execution: big.NewInt(5_000_000_000)},
		{name: "AttributedUsesResolvedCap", url: "https://b.example", value: 4, payment: 9, config: &gloas.BuilderConfig{MinBid: 0, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 99), entry("https://b.example", 6, 1)}}, reject: true},
		{name: "AttributedUsesResolvedCapToPass", url: "https://b.example", value: 4, payment: 2, header: "4000000000", config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 0), entry("https://b.example", 6, 2)}}, execution: big.NewInt(4_000_000_000)},
		{name: "UnmatchedBuilderWithholdsMetadata", url: "https://unknown.example", value: 10, config: &gloas.BuilderConfig{MinBid: 10}},
		{name: "SelfBuiltRetainsValue", builder: gloas.BuilderIndex(^uint64(0)), config: &gloas.BuilderConfig{MinBid: 10}, execution: big.NewInt(42)},
	}
	for _, test := range tests {
		for _, jsonResponse := range []bool{true, false} {
			name := test.name + "/SSZ"
			if jsonResponse {
				name = test.name + "/JSON"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				mockService, err := mock.New(ctx)
				require.NoError(t, err)
				include := false
				mocked, err := mockService.EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include})
				require.NoError(t, err)
				bid := mocked.Data.Gloas.Body.SignedExecutionPayloadBid.Message
				bid.BuilderIndex, bid.Value, bid.ExecutionPayment = test.builder, test.value, test.payment
				var block []byte
				if jsonResponse {
					block, err = json.Marshal(mocked.Data.Gloas)
				} else {
					block, err = mocked.Data.Gloas.MarshalSSZ()
				}
				require.NoError(t, err)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/eth/v1/node/syncing":
						_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
					case "/eth/v1/node/version":
						_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
					case "/eth/v4/validator/blocks/123":
						w.Header().Set("Eth-Consensus-Version", "gloas")
						if !jsonResponse {
							w.Header().Set("Content-Type", "application/octet-stream")
							w.Header().Set("Eth-Execution-Payload-Included", "false")
						}
						value := test.header
						if value == "" {
							value = "42"
						}
						w.Header().Set("Eth-Execution-Payload-Value", value)
						if test.url != "" {
							w.Header().Add("Eth-Builder-Url", test.url)
						}
						if test.url2 != "" {
							w.Header().Add("Eth-Builder-Url", test.url2)
						}
						if jsonResponse {
							_, _ = w.Write(append([]byte(`{"version":"gloas","execution_payload_value":"42","execution_payload_included":false,"data":`), append(block, '}')...))
						} else {
							_, _ = w.Write(block)
						}
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(jsonResponse), eth2http.WithLogLevel(zerolog.Disabled))
				require.NoError(t, err)
				response, err := service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include, BuilderConfig: test.config})
				if test.reject {
					require.ErrorIs(t, err, client.ErrInconsistentResult)
					require.ErrorContains(t, err, "execution payload bid below minimum")
					return
				}
				require.NoError(t, err)
				require.Equal(t, test.execution, response.Data.ExecutionValue)
				if test.execution == nil {
					require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
					require.NotContains(t, response.Metadata, "execution_payload_value")
				} else {
					require.Contains(t, response.Metadata, "Eth-Execution-Payload-Value")
					if jsonResponse {
						require.Contains(t, response.Metadata, "execution_payload_value")
					}
				}
			})
		}
	}
}
