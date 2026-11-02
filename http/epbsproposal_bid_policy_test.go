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
	"strings"
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
		result := validEPBSBuilderConfig().Builders[0]
		result.URL = []byte(url)
		result.MinBid = floor
		result.MaxExecutionPayment = paymentCap
		return result
	}
	const direct = "https://builder.example"
	const other = "https://other.example"
	tests := []struct {
		name                   string
		urls                   []string
		entries                []*gloas.BuilderEntry
		floor, value, payment  phase0.Gwei
		self, reject, withheld bool
	}{
		{name: "UnknownURLBelowFloor", urls: []string{other}, floor: 10, value: 1, reject: true},
		{name: "DuplicateURLBelowFloor", urls: []string{direct}, entries: []*gloas.BuilderEntry{entry(direct, 0, 0), entry(direct, 0, 0)}, floor: 10, value: 1, reject: true},
		{name: "RepeatedURLBelowFloor", urls: []string{direct, other}, entries: []*gloas.BuilderEntry{entry(direct, 0, 0)}, floor: 10, value: 1, reject: true},
		{name: "JoinedURLMatchesEntry", urls: []string{direct, other}, entries: []*gloas.BuilderEntry{entry(direct+";"+other, 0, 0)}, floor: 10, value: 1, reject: true},
		{name: "P2PPaymentBelowFloor", floor: 10, value: 1, payment: 1, reject: true},
		{name: "UnknownURLCap", urls: []string{other}, entries: []*gloas.BuilderEntry{entry(direct, 0, 1), entry("https://third.example", 0, 3)}, floor: 13, value: 10, payment: 5, withheld: true},
		{name: "P2PPaymentCap", entries: []*gloas.BuilderEntry{entry(direct, 0, 1), entry(other, 0, 3)}, floor: 13, value: 10, payment: 5, withheld: true},
		{name: "P2PPaymentAboveCap", entries: []*gloas.BuilderEntry{entry(direct, 0, 1), entry(other, 0, 2)}, floor: 13, value: 10, payment: 5, reject: true},
		{name: "DirectUsesEntryFloorAndCap", urls: []string{direct}, entries: []*gloas.BuilderEntry{entry(other, 0, 0), entry(direct, 12, 2)}, floor: 100, value: 10, payment: 2},
		{name: "DirectDoesNotUseOtherCap", urls: []string{direct}, entries: []*gloas.BuilderEntry{entry(other, 0, 5), entry(direct, 13, 2)}, value: 10, payment: 5, reject: true},
		{name: "SelfBuildRetainsValue", self: true, floor: 100},
		{name: "P2PValueRetained", floor: 10, value: 10},
	}
	for _, encoding := range []string{"json", "ssz"} {
		for _, test := range tests {
			t.Run(encoding+"/"+test.name, func(t *testing.T) {
				ctx := context.Background()
				mockService, err := mock.New(ctx)
				require.NoError(t, err)
				include := false
				mockResponse, err := mockService.EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include})
				require.NoError(t, err)
				block := mockResponse.Data.Gloas
				bid := block.Body.SignedExecutionPayloadBid.Message
				bid.BuilderIndex = 1
				if test.self {
					bid.BuilderIndex = gloas.BuilderIndex(^uint64(0))
				}
				bid.Value, bid.ExecutionPayment = test.value, test.payment
				var body []byte
				if encoding == "json" {
					data, marshalErr := json.Marshal(block)
					require.NoError(t, marshalErr)
					body = append([]byte(`{"version":"gloas","execution_payload_included":false,"execution_payload_value":"10000000000","data":`), data...)
					body = append(body, '}')
				} else {
					body, err = block.MarshalSSZ()
					require.NoError(t, err)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/eth/v1/node/syncing":
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
					case "/eth/v1/node/version":
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
					case "/eth/v4/validator/blocks/123":
						if encoding == "json" {
							w.Header().Set("Content-Type", "application/json")
						} else {
							w.Header().Set("Content-Type", "application/octet-stream")
						}
						w.Header().Set("Eth-Consensus-Version", "gloas")
						w.Header().Set("Eth-Execution-Payload-Included", "false")
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
				service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(encoding == "json"), eth2http.WithLogLevel(zerolog.Disabled))
				require.NoError(t, err)
				response, err := service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{MinBid: test.floor, Builders: test.entries}})
				if test.reject {
					require.ErrorIs(t, err, client.ErrInconsistentResult)
					require.ErrorContains(t, err, "below minimum")
					return
				}
				require.NoError(t, err)
				if test.withheld {
					require.Nil(t, response.Data.ExecutionValue)
					for key := range response.Metadata {
						require.False(t, strings.EqualFold(key, "Eth-Execution-Payload-Value") || strings.EqualFold(key, "execution_payload_value"), key)
					}
				} else {
					require.Equal(t, big.NewInt(10000000000), response.Data.ExecutionValue)
					require.NotEmpty(t, response.Metadata["Eth-Execution-Payload-Value"])
					if encoding == "json" {
						require.Equal(t, "10000000000", response.Metadata["execution_payload_value"])
					}
				}
				require.NotNil(t, response.Data.Value())
			})
		}
	}
}
