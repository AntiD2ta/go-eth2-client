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

func TestEPBSProposalRejectsUnmatchedBuilderBelowMinimum(t *testing.T) {
	_, err := requestPolicyProposal(t, &gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1}, &gloas.BuilderConfig{MinBid: 10}, "https://unknown.example", "")
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "execution payload bid below minimum")
}

func TestEPBSProposalRejectsP2PPaymentBelowMinimum(t *testing.T) {
	_, err := requestPolicyProposal(t, &gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1, ExecutionPayment: 1}, &gloas.BuilderConfig{MinBid: 10}, "", "")
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "execution payload bid below minimum")
}

func TestEPBSProposalJoinedBuilderHeadersCannotAttributeBid(t *testing.T) {
	joined := "https://one.example;https://two.example"
	entry := validEPBSBuilderConfig().Builders[0]
	entry.URL = []byte(joined)
	entry.MinBid = 0
	config := &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry}}
	_, err := requestPolicyProposal(t, &gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 1}, config, joined, "")
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "execution payload bid below minimum")
}

func TestEPBSProposalSelfBuildKeepsExecutionValue(t *testing.T) {
	response, err := requestPolicyProposal(t, &gloas.ExecutionPayloadBid{BuilderIndex: ^gloas.BuilderIndex(0)}, &gloas.BuilderConfig{MinBid: 10}, "", "123")
	require.NoError(t, err)
	require.Equal(t, "123", response.Data.ExecutionValue.String())
	require.Equal(t, "123", response.Metadata["Eth-Execution-Payload-Value"])
	require.Equal(t, "123", response.Metadata["execution_payload_value"])
	require.Equal(t, 0, response.Data.Value().Cmp(response.Data.ExecutionValue))
}

func TestEPBSProposalWithheldValueRemovedFromMetadata(t *testing.T) {
	response, err := requestPolicyProposal(t, &gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 10}, &gloas.BuilderConfig{MinBid: 10}, "https://unknown.example", "123")
	require.NoError(t, err)
	require.Nil(t, response.Data.ExecutionValue)
	require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
	require.NotContains(t, response.Metadata, "execution_payload_value")
}

func TestEPBSProposalSSZWithheldValueRemovedFromMetadata(t *testing.T) {
	response, err := requestPolicyProposalWithEncoding(t, &gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 10}, &gloas.BuilderConfig{MinBid: 10}, "https://unknown.example", "123", false)
	require.NoError(t, err)
	require.Nil(t, response.Data.ExecutionValue)
	require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
}

func requestPolicyProposal(t *testing.T, bid *gloas.ExecutionPayloadBid, config *gloas.BuilderConfig, builderURL, executionValue string) (*api.Response[*api.VersionedEPBSProposal], error) {
	t.Helper()
	return requestPolicyProposalWithEncoding(t, bid, config, builderURL, executionValue, true)
}

func requestPolicyProposalWithEncoding(t *testing.T, bid *gloas.ExecutionPayloadBid, config *gloas.BuilderConfig, builderURL, executionValue string, jsonEncoding bool) (*api.Response[*api.VersionedEPBSProposal], error) {
	t.Helper()
	ctx := context.Background()
	mockService, err := mock.New(ctx)
	require.NoError(t, err)
	include := false
	fixture, err := mockService.EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include})
	require.NoError(t, err)
	fixtureBid := fixture.Data.Gloas.Body.SignedExecutionPayloadBid.Message
	fixtureBid.BuilderIndex = bid.BuilderIndex
	fixtureBid.Value = bid.Value
	fixtureBid.ExecutionPayment = bid.ExecutionPayment
	block, err := json.Marshal(fixture.Data.Gloas)
	require.NoError(t, err)
	if !jsonEncoding {
		block, err = fixture.Data.Gloas.MarshalSSZ()
		require.NoError(t, err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			fmt.Fprint(w, `{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`)
		case "/eth/v1/node/version":
			fmt.Fprint(w, `{"data":{"version":"test"}}`)
		case "/eth/v4/validator/blocks/123":
			w.Header().Set("Eth-Consensus-Version", "gloas")
			if builderURL != "" {
				for _, value := range strings.Split(builderURL, ";") {
					w.Header().Add("Eth-Builder-Url", value)
				}
			}
			if executionValue != "" {
				w.Header().Set("Eth-Execution-Payload-Value", executionValue)
			}
			if jsonEncoding {
				fmt.Fprintf(w, `{"version":"gloas","execution_payload_included":false,"execution_payload_value":%q,"data":%s}`, executionValue, block)
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
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(jsonEncoding), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	return service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
		Slot: 123, IncludePayload: &include, BuilderConfig: config,
	})
}

func TestEPBSProposalBidPolicy(t *testing.T) {
	entry := func(url string, floor, paymentCap phase0.Gwei) *gloas.BuilderEntry {
		builder := validEPBSBuilderConfig().Builders[0]
		builder.URL = []byte(url)
		builder.MinBid = floor
		builder.MaxExecutionPayment = paymentCap
		return builder
	}
	tests := []struct {
		name      string
		url       string
		bid       gloas.ExecutionPayloadBid
		config    *gloas.BuilderConfig
		value     string
		wantError bool
		withheld  bool
	}{
		{
			name: "DuplicateURLBelowFloor", url: "https://same.example",
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 9},
			config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{
				entry("https://same.example", 0, 2), entry("https://same.example", 0, 5),
			}}, wantError: true,
		},
		{
			name: "DuplicateURLUsesMaximumCap", url: "https://same.example", value: "9000000000",
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 9, ExecutionPayment: 3},
			config: &gloas.BuilderConfig{MinBid: 12, Builders: []*gloas.BuilderEntry{
				entry("https://same.example", 0, 1), entry("https://same.example", 0, 5),
			}}, withheld: true,
		},
		{
			name: "UnmatchedUsesMaximumCapNotSum", url: "https://unknown.example",
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 5, ExecutionPayment: 9},
			config: &gloas.BuilderConfig{MinBid: 11, Builders: []*gloas.BuilderEntry{
				entry("https://one.example", 0, 3), entry("https://two.example", 0, 5),
			}}, wantError: true,
		},
		{
			name: "P2PPaymentUsesMaximumCap", value: "9000000000",
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 9, ExecutionPayment: 3},
			config: &gloas.BuilderConfig{MinBid: 12, Builders: []*gloas.BuilderEntry{
				entry("https://one.example", 0, 1), entry("https://two.example", 0, 3),
			}}, withheld: true,
		},
		{
			name: "AttributedEntryFloorOverridesTopLevel", url: "https://two.example", value: "5000000000",
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 5},
			config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{
				entry("https://one.example", 50, 0), entry("https://two.example", 5, 0),
			}},
		},
		{
			name: "AttributedUsesItsOwnCap", url: "https://two.example",
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 10, ExecutionPayment: 4},
			config: &gloas.BuilderConfig{MinBid: 0, Builders: []*gloas.BuilderEntry{
				entry("https://one.example", 0, 100), entry("https://two.example", 13, 2),
			}}, wantError: true,
		},
		{
			name: "AttributedCapAtBoundary", url: "https://two.example", value: "12000000000",
			bid: gloas.ExecutionPayloadBid{BuilderIndex: 1, Value: 10, ExecutionPayment: 4},
			config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{
				entry("https://one.example", 0, 100), entry("https://two.example", 12, 2),
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := requestPolicyProposal(t, &test.bid, test.config, test.url, test.value)
			if test.wantError {
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
				require.Equal(t, test.value, response.Data.ExecutionValue.String())
			}
		})
	}
}
