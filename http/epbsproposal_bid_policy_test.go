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
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func bidPolicyEntry(url string, floor, paymentCap phase0.Gwei) *gloas.BuilderEntry {
	return &gloas.BuilderEntry{
		URL: []byte(url), MinBid: floor, MaxExecutionPayment: paymentCap,
		Auth: &gloas.SignedBuilderRequestAuth{Message: &gloas.BuilderRequestAuth{Slot: 60300, Data: []byte("auth")}},
	}
}

func TestEPBSProposalBidPolicy(t *testing.T) {
	first := bidPolicyEntry("https://first.example", 5, 1)
	second := bidPolicyEntry("https://second.example", 7, 3)
	duplicate := bidPolicyEntry("https://first.example", 5, 3)
	joined := bidPolicyEntry("https://first.example;https://second.example", 1, 10)
	tests := []struct {
		name             string
		config           *gloas.BuilderConfig
		urls             []string
		value, payment   phase0.Gwei
		builderIndex     gloas.BuilderIndex
		headerValue      string
		included         bool
		reject, withheld bool
	}{
		{name: "UnmatchedBelow", config: &gloas.BuilderConfig{MinBid: 10}, urls: []string{"https://unknown.example"}, value: 1, reject: true},
		{name: "DuplicateBelow", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{first, duplicate}}, urls: []string{"https://first.example"}, value: 6, payment: 3, reject: true},
		{name: "JoinedBelow", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{first, second}}, urls: []string{"https://first.example", "https://second.example"}, value: 6, payment: 3, reject: true},
		{name: "JoinedMatchesEntryStillAmbiguous", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{joined}}, urls: []string{"https://first.example", "https://second.example"}, value: 1, reject: true},
		{name: "P2PPaymentBelow", config: &gloas.BuilderConfig{MinBid: 10}, value: 1, payment: 1, reject: true},
		{name: "P2PPaymentAtFloorUsesLargestCap", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{first, second}}, value: 7, payment: 5, headerValue: "12000000000", withheld: true},
		{name: "UnknownAtFloorUsesLargestCap", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{first, second}}, urls: []string{"https://unknown.example"}, value: 7, payment: 5, headerValue: "12000000000", withheld: true},
		{name: "DuplicateAtFloorUsesLargestCap", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{first, duplicate}}, urls: []string{"https://first.example"}, value: 7, payment: 3, headerValue: "10000000000", withheld: true},
		{name: "UnknownWithoutHeader", config: &gloas.BuilderConfig{MinBid: 1}, urls: []string{"https://unknown.example"}, value: 1, withheld: true},
		{name: "P2PWithoutPayment", config: &gloas.BuilderConfig{MinBid: 10}, value: 10, headerValue: "10000000000"},
		{name: "AttributedUsesEntryFloorAndCap", config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{first, second}}, urls: []string{"https://second.example"}, value: 4, payment: 4, headerValue: fmt.Sprint(uint64(7) * 1_000_000_000)},
		{name: "AttributedUsesFirstCap", config: &gloas.BuilderConfig{MinBid: 0, Builders: []*gloas.BuilderEntry{first, second}}, urls: []string{"https://first.example"}, value: 3, payment: 4, reject: true},
		{name: "SelfBuildKeepsValue", config: &gloas.BuilderConfig{MinBid: 100}, builderIndex: staticBuilderIndexSelfBuild, headerValue: "12345"},
		{name: "SelfBuildIncludedKeepsValue", config: &gloas.BuilderConfig{MinBid: 100}, builderIndex: staticBuilderIndexSelfBuild, headerValue: "12345", included: true},
	}
	for _, format := range []string{"json", "ssz"} {
		for _, test := range tests {
			t.Run(format+"/"+test.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				block := validEPBSBeaconBlock()
				contents := validEPBSBlockContents()
				if test.included {
					block = contents.Block
				}
				bid := block.Body.SignedExecutionPayloadBid.Message
				bid.BuilderIndex = test.builderIndex
				if test.builderIndex == 0 {
					bid.BuilderIndex = 1
				}
				bid.Value, bid.ExecutionPayment = test.value, test.payment
				var data []byte
				var err error
				if test.included {
					contents.ExecutionPayloadEnvelope.BuilderIndex = bid.BuilderIndex
					root, rootErr := block.HashTreeRoot()
					require.NoError(t, rootErr)
					contents.ExecutionPayloadEnvelope.BeaconBlockRoot = root
					if format == "json" {
						data, err = json.Marshal(contents)
					} else {
						data, err = contents.MarshalSSZ()
					}
				} else if format == "json" {
					data, err = json.Marshal(block)
				} else {
					data, err = block.MarshalSSZ()
				}
				require.NoError(t, err)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/eth/v1/node/version":
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"data":{"version":"test"}}`)
					case "/eth/v1/node/syncing":
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`)
					case "/eth/v4/validator/blocks/60300":
						w.Header().Set("Eth-Consensus-Version", "gloas")
						w.Header().Set("Eth-Consensus-Block-Value", "42")
						if test.headerValue != "" {
							w.Header().Set("Eth-Execution-Payload-Value", test.headerValue)
						}
						for _, url := range test.urls {
							w.Header().Add("Eth-Builder-Url", url)
						}
						if format == "json" {
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(w, `{"version":"gloas","execution_payload_included":%t,"execution_payload_value":"98765","consensus_block_value":"0","data":%s}`, test.included, data)
						} else {
							w.Header().Set("Content-Type", "application/octet-stream")
							w.Header().Set("Eth-Execution-Payload-Included", fmt.Sprint(test.included))
							_, _ = w.Write(data)
						}
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				service, err := New(ctx, WithAddress(server.URL), WithEnforceJSON(format == "json"), WithLogLevel(zerolog.Disabled))
				require.NoError(t, err)
				included := test.included
				response, err := service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
					Slot: block.Slot, RandaoReveal: block.Body.RANDAOReveal, BuilderConfig: test.config, IncludePayload: &included,
				})
				if test.reject {
					require.ErrorIs(t, err, client.ErrInconsistentResult)
					require.ErrorContains(t, err, "below minimum")
					return
				}
				require.NoError(t, err)
				if test.withheld {
					require.Nil(t, response.Data.ExecutionValue)
					for key := range response.Metadata {
						require.False(t, strings.EqualFold(key, "Eth-Execution-Payload-Value") || strings.EqualFold(key, "execution_payload_value"), "metadata retains %s", key)
					}
					require.Equal(t, big.NewInt(42), response.Data.Value())
				} else {
					expected, ok := new(big.Int).SetString(test.headerValue, 10)
					require.True(t, ok)
					require.Equal(t, expected, response.Data.ExecutionValue)
					require.Equal(t, new(big.Int).Add(expected, big.NewInt(42)), response.Data.Value())
					require.Contains(t, response.Metadata, "Eth-Execution-Payload-Value")
				}
				require.Contains(t, response.Metadata, "Eth-Consensus-Block-Value")
			})
		}
	}
}
