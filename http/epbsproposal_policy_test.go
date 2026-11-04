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

func TestEPBSProposalBidMinimum(t *testing.T) {
	entry := func(url string, floor, paymentCap phase0.Gwei) *gloas.BuilderEntry {
		return &gloas.BuilderEntry{URL: []byte(url), MinBid: floor, MaxExecutionPayment: paymentCap,
			Auth: &gloas.SignedBuilderRequestAuth{Message: &gloas.BuilderRequestAuth{Slot: 60300, Data: []byte("auth")}}}
	}
	tests := []struct {
		name             string
		urls             []string
		config           *gloas.BuilderConfig
		value, payment   phase0.Gwei
		reject, withheld bool
		self, ssz        bool
	}{
		{name: "self build retains reported value", config: &gloas.BuilderConfig{MinBid: 100}, self: true},
		{name: "unmatched", urls: []string{"https://unknown.example"}, config: &gloas.BuilderConfig{MinBid: 10}, value: 1, reject: true},
		{name: "duplicate", urls: []string{"https://a.example"}, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://a.example", 0, 2)}}, value: 1, reject: true},
		{name: "joined", urls: []string{"https://a.example", "https://b.example"}, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://b.example", 0, 2)}}, value: 1, reject: true},
		{name: "joined matches configured URL", urls: []string{"https://a.example", "https://b.example"}, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example;https://b.example", 0, 0)}}, value: 1, reject: true},
		{name: "p2p payment below floor", config: &gloas.BuilderConfig{MinBid: 10}, value: 1, payment: 1, reject: true},
		{name: "unmatched value withheld JSON", urls: []string{"https://unknown.example"}, config: &gloas.BuilderConfig{MinBid: 1}, value: 1, withheld: true},
		{name: "unmatched value withheld SSZ", urls: []string{"https://unknown.example"}, config: &gloas.BuilderConfig{MinBid: 1}, value: 1, withheld: true, ssz: true},
		{name: "p2p payment max cap", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://b.example", 0, 4)}}, value: 6, payment: 8, withheld: true},
		{name: "p2p capped below floor", config: &gloas.BuilderConfig{MinBid: 11, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://b.example", 0, 4)}}, value: 6, payment: 8, reject: true},
		{name: "direct uses entry floor and cap", urls: []string{"https://b.example"}, config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{entry("https://a.example", 99, 0), entry("https://b.example", 10, 4)}}, value: 6, payment: 8},
		{name: "direct cap not other entry", urls: []string{"https://a.example"}, config: &gloas.BuilderConfig{MinBid: 0, Builders: []*gloas.BuilderEntry{entry("https://a.example", 10, 1), entry("https://b.example", 0, 8)}}, value: 6, payment: 8, reject: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			block := validEPBSBeaconBlock()
			bid := block.Body.SignedExecutionPayloadBid.Message
			bid.BuilderIndex, bid.Value, bid.ExecutionPayment = 1, test.value, test.payment
			if test.self {
				bid.BuilderIndex = gloas.BuilderIndex(^uint64(0))
			}
			body, _ := epbsProposalJSONBody(t, false, block)
			if test.ssz {
				var err error
				body, err = block.MarshalSSZ()
				require.NoError(t, err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/eth/v1/node/version":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
				case "/eth/v1/node/syncing":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
				case "/eth/v4/validator/blocks/60300":
					w.Header().Set("Content-Type", "application/json")
					if test.ssz {
						w.Header().Set("Content-Type", "application/octet-stream")
						w.Header().Set("Eth-Execution-Payload-Included", "false")
					}
					w.Header().Set("Eth-Consensus-Version", "gloas")
					w.Header().Set("Eth-Execution-Payload-Value", "10000000000")
					for _, url := range test.urls {
						w.Header().Add("Eth-Builder-Url", url)
					}
					_, _ = w.Write(body)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			ctx := context.Background()
			service, err := New(ctx, WithAddress(server.URL), WithEnforceJSON(!test.ssz), WithLogLevel(zerolog.Disabled))
			require.NoError(t, err)
			included := false
			response, err := service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
				Slot: block.Slot, RandaoReveal: block.Body.RANDAOReveal,
				IncludePayload: &included, BuilderConfig: test.config,
			})
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
			} else {
				require.Equal(t, "10000000000", response.Data.ExecutionValue.String())
				require.Contains(t, response.Metadata, "Eth-Execution-Payload-Value")
				if !test.ssz {
					require.Contains(t, response.Metadata, "execution_payload_value")
				}
			}
		})
	}
}
