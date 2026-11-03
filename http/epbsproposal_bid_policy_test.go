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
	type entry struct {
		url          string
		minimum, cap phase0.Gwei
	}
	first := entry{url: "https://first.example", minimum: 3, cap: 1}
	second := entry{url: "https://second.example", minimum: 10, cap: 7}
	duplicate := entry{url: "https://first.example", cap: 7}
	tests := []struct {
		name                          string
		urls                          []string
		entries                       []entry
		value, payment, minimum       phase0.Gwei
		self, ssz, rejected, withheld bool
	}{
		{name: "UnmatchedBelowFloor", urls: []string{"https://unknown.example"}, value: 1, minimum: 10, rejected: true},
		{name: "DuplicateBelowFloor", urls: []string{first.url}, entries: []entry{first, duplicate}, value: 1, minimum: 10, rejected: true},
		{name: "RepeatedHeadersBelowFloor", urls: []string{first.url, second.url}, entries: []entry{first, second}, value: 1, minimum: 10, rejected: true},
		{name: "JoinedHeaderCannotMatchEntry", urls: []string{"https://first.example;https://second.example"}, entries: []entry{{url: "https://first.example;https://second.example", minimum: 1}}, value: 1, minimum: 10, rejected: true},
		{name: "P2PPaymentBelowFloor", value: 1, payment: 1, minimum: 10, rejected: true},
		{name: "P2PUsesLargestCap", entries: []entry{first, second}, value: 4, payment: 9, minimum: 11, withheld: true},
		{name: "P2PAboveLargestCap", entries: []entry{first, second}, value: 4, payment: 9, minimum: 12, rejected: true},
		{name: "UnmatchedUsesLargestCap", urls: []string{"https://unknown.example"}, entries: []entry{first, second}, value: 4, payment: 9, minimum: 11, withheld: true},
		{name: "UnmatchedSSZWithholdsHeader", urls: []string{"https://unknown.example"}, value: 10, minimum: 10, ssz: true, withheld: true},
		{name: "DuplicateWithholdsValue", urls: []string{first.url}, entries: []entry{first, duplicate}, value: 10, minimum: 10, withheld: true},
		{name: "DirectUsesEntryFloorNotP2PFloor", urls: []string{first.url}, entries: []entry{first, second}, value: 3, minimum: 100},
		{name: "DirectUsesSelectedEntryCap", urls: []string{second.url}, entries: []entry{first, second}, value: 4, payment: 6, minimum: 100},
		{name: "DirectEntryFloorRejects", urls: []string{second.url}, entries: []entry{first, second}, value: 4, payment: 5, minimum: 1, rejected: true},
		{name: "P2PNoPaymentVerified", value: 10, minimum: 10},
		{name: "SelfBuildKeepsValue", self: true, minimum: 100},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			mockService, err := mock.New(ctx)
			require.NoError(t, err)
			included := false
			proposal, err := mockService.EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &included})
			require.NoError(t, err)
			bid := proposal.Data.Gloas.Body.SignedExecutionPayloadBid.Message
			bid.BuilderIndex = 1
			if test.self {
				bid.BuilderIndex = gloas.BuilderIndex(^uint64(0))
			}
			bid.Value = test.value
			bid.ExecutionPayment = test.payment
			reported := uint64(test.value) * 1_000_000_000
			if test.self {
				reported = 12_000_000_000
			}
			var block []byte
			if test.ssz {
				block, err = proposal.Data.Gloas.MarshalSSZ()
			} else {
				block, err = json.Marshal(proposal.Data.Gloas)
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
					w.Header().Set("Eth-Execution-Payload-Value", fmt.Sprint(reported))
					w.Header().Set("Eth-Consensus-Block-Value", "7")
					for _, url := range test.urls {
						w.Header().Add("Eth-Builder-Url", url)
					}
					if test.ssz {
						w.Header().Set("Content-Type", "application/octet-stream")
						w.Header().Set("Eth-Execution-Payload-Included", "false")
						_, _ = w.Write(block)
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = fmt.Fprintf(w, `{"version":"gloas","execution_payload_value":"%d","execution_payload_included":false,"data":%s}`, reported, block)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(!test.ssz), eth2http.WithLogLevel(zerolog.Disabled))
			require.NoError(t, err)
			config := &gloas.BuilderConfig{MinBid: test.minimum}
			for _, item := range test.entries {
				e := validEPBSBuilderConfig().Builders[0]
				e.URL, e.MinBid, e.MaxExecutionPayment = []byte(item.url), item.minimum, item.cap
				config.Builders = append(config.Builders, e)
			}
			response, err := service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &included, BuilderConfig: config})
			if test.rejected {
				require.ErrorIs(t, err, client.ErrInconsistentResult)
				require.ErrorContains(t, err, "execution payload bid below minimum")
				return
			}
			require.NoError(t, err)
			require.Equal(t, "7", response.Metadata["Eth-Consensus-Block-Value"])
			if test.withheld {
				require.Nil(t, response.Data.ExecutionValue)
				require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
				require.NotContains(t, response.Metadata, "execution_payload_value")
				require.Equal(t, big.NewInt(7), response.Data.Value())
			} else {
				require.Equal(t, new(big.Int).SetUint64(reported), response.Data.ExecutionValue)
				require.Equal(t, fmt.Sprint(reported), response.Metadata["Eth-Execution-Payload-Value"])
				if !test.ssz {
					require.Equal(t, fmt.Sprint(reported), response.Metadata["execution_payload_value"])
				}
				require.Equal(t, new(big.Int).SetUint64(reported+7), response.Data.Value())
			}
		})
	}
}
