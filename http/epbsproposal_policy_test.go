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
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func policyEntry(url string, floor, maxPayment phase0.Gwei) *gloas.BuilderEntry {
	return &gloas.BuilderEntry{URL: []byte(url), MinBid: floor, MaxExecutionPayment: maxPayment,
		Auth: &gloas.SignedBuilderRequestAuth{Message: &gloas.BuilderRequestAuth{Slot: 60300, Data: []byte{1}}},
	}
}

func policyProposal(t *testing.T, config *gloas.BuilderConfig, urls []string, index gloas.BuilderIndex, bidValue, payment phase0.Gwei, jsonResponse bool) (*api.Response[*api.VersionedEPBSProposal], error) {
	t.Helper()
	block := validEPBSBeaconBlock()
	block.Body.SignedExecutionPayloadBid.Message.BuilderIndex = index
	block.Body.SignedExecutionPayloadBid.Message.Value = bidValue
	block.Body.SignedExecutionPayloadBid.Message.ExecutionPayment = payment
	var body []byte
	var err error
	if jsonResponse {
		body, err = json.Marshal(block)
		require.NoError(t, err)
		body = fmt.Appendf(nil, `{"version":"gloas","consensus_block_value":"3","execution_payload_value":"12345","execution_payload_included":false,"data":%s}`, body)
	} else {
		body, err = block.MarshalSSZ()
		require.NoError(t, err)
	}
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
		case "/eth/v1/node/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v4/validator/blocks/60300":
			if jsonResponse {
				w.Header().Set("Content-Type", "application/json")
			} else {
				w.Header().Set("Content-Type", "application/octet-stream")
			}
			w.Header().Set("Eth-Consensus-Version", "gloas")
			w.Header().Set("Eth-Execution-Payload-Included", "false")
			executionValue := uint64(bidValue) * 1_000_000_000
			if index == staticBuilderIndexSelfBuild {
				executionValue = 12345
			}
			w.Header().Set("Eth-Execution-Payload-Value", fmt.Sprint(executionValue))
			w.Header().Set("Eth-Consensus-Block-Value", "3")
			for _, url := range urls {
				w.Header().Add("Eth-Builder-Url", url)
			}
			_, _ = w.Write(body)
		default:
			nethttp.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service, err := New(ctx, WithAddress(server.URL), WithAllowDelayedStart(true), WithEnforceJSON(jsonResponse), WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	include := false
	return service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 60300, RandaoReveal: block.Body.RANDAOReveal, BuilderConfig: config, IncludePayload: &include})
}

func TestEPBSProposalBidFloors(t *testing.T) {
	entries := []*gloas.BuilderEntry{policyEntry("https://a.example", 4, 1), policyEntry("https://b.example", 12, 5)}
	duplicate := []*gloas.BuilderEntry{policyEntry("https://a.example", 1, 1), policyEntry("https://a.example", 1, 5)}
	tests := []struct {
		name           string
		entries        []*gloas.BuilderEntry
		urls           []string
		value, payment phase0.Gwei
		reject         bool
	}{
		{name: "UnmatchedBelowFloor", urls: []string{"https://unknown.example"}, value: 1, reject: true},
		{name: "DuplicateBelowFloor", entries: duplicate, urls: []string{"https://a.example"}, value: 1, reject: true},
		{name: "RepeatedHeadersBelowFloor", entries: entries, urls: []string{"https://a.example", "https://b.example"}, value: 1, reject: true},
		{name: "P2PPaymentBelowFloor", value: 1, payment: 1, reject: true},
		{name: "P2PUsesLargestCap", entries: entries, value: 5, payment: 5},
		{name: "P2PRejectsBeyondLargestCap", entries: entries, value: 4, payment: 100, reject: true},
		{name: "UnknownUsesLargestCap", entries: entries, urls: []string{"https://unknown.example"}, value: 5, payment: 5},
		{name: "AttributedUsesEntryFloor", entries: entries, urls: []string{"https://a.example"}, value: 4},
		{name: "AttributedUsesMatchedCap", entries: entries, urls: []string{"https://b.example"}, value: 7, payment: 5},
		{name: "AttributedRejectsBeyondMatchedCap", entries: entries, urls: []string{"https://a.example"}, value: 2, payment: 5, reject: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response, err := policyProposal(t, &gloas.BuilderConfig{MinBid: 10, Builders: tc.entries}, tc.urls, 1, tc.value, tc.payment, true)
			if tc.reject {
				require.ErrorIs(t, err, client.ErrInconsistentResult)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, response)
		})
	}
}

func TestEPBSProposalValueVisibility(t *testing.T) {
	for _, jsonResponse := range []bool{false, true} {
		t.Run(fmt.Sprintf("JSON_%t", jsonResponse), func(t *testing.T) {
			t.Run("SelfBuild", func(t *testing.T) {
				response, err := policyProposal(t, &gloas.BuilderConfig{MinBid: 100}, nil, staticBuilderIndexSelfBuild, 0, 0, jsonResponse)
				require.NoError(t, err)
				require.Equal(t, big.NewInt(12345), response.Data.ExecutionValue)
				require.Equal(t, big.NewInt(12348), response.Data.Value())
				require.Equal(t, "12345", response.Metadata["Eth-Execution-Payload-Value"])
			})
			for _, tc := range []struct {
				name    string
				urls    []string
				payment phase0.Gwei
			}{
				{name: "Unmatched", urls: []string{"https://unknown.example"}},
				{name: "P2PPayment", payment: 1},
			} {
				t.Run(tc.name, func(t *testing.T) {
					response, err := policyProposal(t, &gloas.BuilderConfig{}, tc.urls, 1, 10, tc.payment, jsonResponse)
					require.NoError(t, err)
					require.Nil(t, response.Data.ExecutionValue)
					require.Equal(t, big.NewInt(3), response.Data.Value())
					require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
					require.NotContains(t, response.Metadata, "execution_payload_value")
					require.Contains(t, response.Metadata, "Eth-Consensus-Block-Value")
				})
			}
		})
	}
}
