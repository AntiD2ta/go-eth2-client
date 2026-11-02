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
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestEPBSProposalBidPolicy(t *testing.T) {
	const slot = phase0.Slot(60300)
	entry := func(url string, floor, paymentCap phase0.Gwei) *gloas.BuilderEntry {
		return &gloas.BuilderEntry{URL: []byte(url), MinBid: floor, MaxExecutionPayment: paymentCap,
			Auth: &gloas.SignedBuilderRequestAuth{Message: &gloas.BuilderRequestAuth{Slot: slot, Data: []byte{1}}}}
	}
	tests := []struct {
		name             string
		headers          []string
		config           *gloas.BuilderConfig
		index            gloas.BuilderIndex
		value, payment   phase0.Gwei
		reported         string
		reject, withheld bool
	}{
		{name: "UnmatchedBelowFloor", headers: []string{"https://unknown.example"}, config: &gloas.BuilderConfig{MinBid: 10}, index: 1, value: 1, reported: "1000000000", reject: true},
		{name: "DuplicateBelowFloor", headers: []string{"https://a.example"}, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 0), entry("https://a.example", 0, 0)}}, index: 1, value: 1, reported: "1000000000", reject: true},
		{name: "JoinedHeadersCannotMatchEntry", headers: []string{"https://a.example", "https://b.example"}, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example;https://b.example", 0, 0)}}, index: 1, value: 1, reported: "1000000000", reject: true},
		{name: "P2PPaymentBelowFloor", config: &gloas.BuilderConfig{MinBid: 10}, index: 1, value: 1, payment: 1, reported: "2000000000", reject: true},
		{name: "UnattributedUsesLargestCap", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://b.example", 0, 3)}}, index: 1, value: 7, payment: 3, reported: "10000000000", withheld: true},
		{name: "UnattributedCapLimitsPayment", config: &gloas.BuilderConfig{MinBid: 11, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://b.example", 0, 3)}}, index: 1, value: 7, payment: 8, reported: "15000000000", reject: true},
		{name: "MatchedEntryFloorAndCap", headers: []string{"https://b.example"}, config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 20), entry("https://b.example", 10, 3)}}, index: 1, value: 7, payment: 3, reported: "10000000000"},
		{name: "MatchedEntryCapNotOtherEntry", headers: []string{"https://b.example"}, config: &gloas.BuilderConfig{MinBid: 1, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 20), entry("https://b.example", 11, 3)}}, index: 1, value: 7, payment: 8, reported: "15000000000", reject: true},
		{name: "SelfBuiltValue", config: &gloas.BuilderConfig{MinBid: 100}, index: staticBuilderIndexSelfBuild, reported: "7000000000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			block := validEPBSBeaconBlock()
			block.Body.SignedExecutionPayloadBid.Message.BuilderIndex = test.index
			block.Body.SignedExecutionPayloadBid.Message.Value = test.value
			block.Body.SignedExecutionPayloadBid.Message.ExecutionPayment = test.payment
			body, _ := epbsProposalJSONBody(t, false, block)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/node/syncing":
					_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
				case "/eth/v1/node/version":
					_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
				case "/eth/v4/validator/blocks/60300":
					w.Header().Set("Eth-Consensus-Version", "gloas")
					w.Header().Set("Eth-Execution-Payload-Value", test.reported)
					for _, header := range test.headers {
						w.Header().Add("Eth-Builder-Url", header)
					}
					_, _ = w.Write(body)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service, err := New(ctx, WithAddress(server.URL), WithEnforceJSON(true), WithLogLevel(zerolog.Disabled))
			require.NoError(t, err)
			included := false
			response, err := service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
				Slot: slot, RandaoReveal: block.Body.RANDAOReveal, BuilderConfig: test.config, IncludePayload: &included,
			})
			if test.reject {
				require.ErrorIs(t, err, client.ErrInconsistentResult)
				require.ErrorContains(t, err, "execution payload bid below minimum")
				return
			}
			require.NoError(t, err)
			if test.withheld {
				require.Nil(t, response.Data.ExecutionValue)
				require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
				require.NotContains(t, response.Metadata, "execution_payload_value")
			} else {
				expected, ok := new(big.Int).SetString(test.reported, 10)
				require.True(t, ok)
				require.Equal(t, expected, response.Data.ExecutionValue)
				require.Equal(t, test.reported, response.Metadata["Eth-Execution-Payload-Value"])
			}
		})
	}
}
