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

func TestEPBSProposalBidPolicy(t *testing.T) {
	builder := func(url string, minimum, maxPayment phase0.Gwei) *gloas.BuilderEntry {
		entry := validEPBSBuilderConfig().Builders[0]
		entry.URL, entry.MinBid, entry.MaxExecutionPayment = []byte(url), minimum, maxPayment
		return entry
	}
	tests := []struct {
		name           string
		urls           []string
		value, payment phase0.Gwei
		config         *gloas.BuilderConfig
		reject         bool
		selfBuild      bool
		withheld       bool
		reported       bool
	}{
		{name: "Unmatched", urls: []string{"https://unknown.example"}, value: 1, config: &gloas.BuilderConfig{MinBid: 10}, reject: true},
		{name: "P2PPayment", value: 1, payment: 1, config: &gloas.BuilderConfig{MinBid: 10}, reject: true},
		{name: "P2PLargestCap", value: 5, payment: 5, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{builder("https://one.example", 0, 2), builder("https://two.example", 0, 5)}}},
		{name: "SelfBuild", selfBuild: true, config: &gloas.BuilderConfig{MinBid: 10}},
		{name: "UnmatchedValueWithheld", urls: []string{"https://unknown.example"}, value: 10, withheld: true, config: &gloas.BuilderConfig{MinBid: 10}},
		{name: "DuplicateURL", urls: []string{"https://same.example"}, value: 1, reject: true, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{builder("https://same.example", 0, 0), builder("https://same.example", 0, 0)}}},
		{name: "JoinedHeaders", urls: []string{"https://one.example", "https://two.example"}, value: 1, reject: true, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{builder("https://one.example", 0, 0), builder("https://two.example", 0, 0)}}},
		{name: "JoinedURLCannotMatch", urls: []string{"https://one.example;https://two.example"}, value: 1, reject: true, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{builder("https://one.example;https://two.example", 0, 0)}}},
		{name: "DirectEntryFloorOnly", urls: []string{"https://two.example"}, value: 5, payment: 3, config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{builder("https://one.example", 100, 1), builder("https://two.example", 8, 3)}}},
		{name: "DirectValueRetained", urls: []string{"https://two.example"}, value: 5, payment: 3, reported: true, config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{builder("https://one.example", 0, 0), builder("https://two.example", 8, 3)}}},
		{name: "DirectEntryMinimum", urls: []string{"https://two.example"}, value: 5, reject: true, config: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{builder("https://one.example", 0, 0), builder("https://two.example", 6, 0)}}},
		{name: "DirectEntryCap", urls: []string{"https://two.example"}, value: 5, payment: 5, reject: true, config: &gloas.BuilderConfig{MinBid: 0, Builders: []*gloas.BuilderEntry{builder("https://one.example", 0, 9), builder("https://two.example", 8, 2)}}},
		{name: "P2PCapLimitsPayment", value: 5, payment: 9, reject: true, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{builder("https://one.example", 0, 2), builder("https://two.example", 0, 4)}}},
		{name: "P2PPaymentValueWithheld", value: 10, payment: 1, withheld: true, config: &gloas.BuilderConfig{MinBid: 10}},
		{name: "UnmatchedUsesLargestCap", urls: []string{"https://unknown.example"}, value: 5, payment: 5, withheld: true, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{builder("https://one.example", 0, 2), builder("https://two.example", 0, 5)}}},
	}
	for _, test := range tests {
		for _, jsonEncoding := range []bool{true, false} {
			name := "SSZ"
			if jsonEncoding {
				name = "JSON"
			}
			t.Run(test.name+"/"+name, func(t *testing.T) {
				include := false
				mockService, err := mock.New(context.Background())
				require.NoError(t, err)
				fixture, err := mockService.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include})
				require.NoError(t, err)
				bid := fixture.Data.Gloas.Body.SignedExecutionPayloadBid.Message
				bid.Value, bid.ExecutionPayment = test.value, test.payment
				if test.selfBuild {
					bid.BuilderIndex = gloas.BuilderIndex(^uint64(0))
				}
				block, err := json.Marshal(fixture.Data.Gloas)
				require.NoError(t, err)
				sszBlock, err := fixture.Data.Gloas.MarshalSSZ()
				require.NoError(t, err)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/eth/v1/node/version":
						fmt.Fprint(w, `{"data":{"version":"test"}}`)
					case "/eth/v1/node/syncing":
						fmt.Fprint(w, `{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`)
					case "/eth/v4/validator/blocks/123":
						w.Header().Set("Eth-Consensus-Version", "gloas")
						if test.selfBuild || test.withheld {
							w.Header().Set("Eth-Execution-Payload-Value", "123")
						} else if test.reported {
							w.Header().Set("Eth-Execution-Payload-Value", fmt.Sprint(8*1_000_000_000))
						}
						for _, url := range test.urls {
							w.Header().Add("Eth-Builder-Url", url)
						}
						if jsonEncoding {
							fmt.Fprintf(w, `{"version":"gloas","execution_payload_included":false,"execution_payload_value":"123","data":%s}`, block)
						} else {
							w.Header().Set("Content-Type", "application/octet-stream")
							w.Header().Set("Eth-Execution-Payload-Included", "false")
							_, _ = w.Write(sszBlock)
						}
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				service, err := eth2http.New(context.Background(), eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(jsonEncoding), eth2http.WithLogLevel(zerolog.Disabled))
				require.NoError(t, err)
				response, err := service.(client.EPBSProposalProvider).EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include, BuilderConfig: test.config})
				if test.reject {
					require.ErrorIs(t, err, client.ErrInconsistentResult)
					require.ErrorContains(t, err, "execution payload bid below minimum")
				} else {
					require.NoError(t, err)
					if test.selfBuild {
						require.Equal(t, "123", response.Data.ExecutionValue.String())
						require.Equal(t, "123", response.Data.Value().String())
						require.Equal(t, "123", response.Metadata["Eth-Execution-Payload-Value"])
					}
					if test.reported {
						want := fmt.Sprint(8 * 1_000_000_000)
						require.Equal(t, want, response.Data.ExecutionValue.String())
						require.Equal(t, want, response.Metadata["Eth-Execution-Payload-Value"])
					}
					if test.withheld {
						require.Nil(t, response.Data.ExecutionValue)
						require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
						require.NotContains(t, response.Metadata, "execution_payload_value")
					}
				}
			})
		}
	}
}
