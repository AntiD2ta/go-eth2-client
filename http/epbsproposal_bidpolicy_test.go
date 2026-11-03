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

func requestEPBSBid(t *testing.T, bid gloas.ExecutionPayloadBid, config *gloas.BuilderConfig, urls ...string) (*api.Response[*api.VersionedEPBSProposal], error) {
	t.Helper()
	return requestEPBSBidFormat(t, bid, config, false, urls...)
}

func requestEPBSBidFormat(t *testing.T, bid gloas.ExecutionPayloadBid, config *gloas.BuilderConfig, ssz bool, urls ...string) (*api.Response[*api.VersionedEPBSProposal], error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mockService, err := mock.New(ctx)
	require.NoError(t, err)
	include := false
	blockResponse, err := mockService.EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include})
	require.NoError(t, err)
	bid.Slot = 123
	blockResponse.Data.Gloas.Body.SignedExecutionPayloadBid.Message = &bid
	block, err := json.Marshal(blockResponse.Data.Gloas)
	require.NoError(t, err)
	if ssz {
		block, err = blockResponse.Data.Gloas.MarshalSSZ()
		require.NoError(t, err)
	}
	reported := new(big.Int).Mul(new(big.Int).SetUint64(uint64(bid.Value)), big.NewInt(1_000_000_000)).String()
	if bid.BuilderIndex == gloas.BuilderIndex(^uint64(0)) {
		reported = "42"
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v4/validator/blocks/123":
			w.Header().Set("Eth-Consensus-Version", "gloas")
			w.Header().Set("Eth-Execution-Payload-Value", reported)
			for _, url := range urls {
				w.Header().Add("Eth-Builder-Url", url)
			}
			if ssz {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Eth-Execution-Payload-Included", "false")
				_, _ = w.Write(block)
			} else {
				_, _ = w.Write(append(append([]byte(fmt.Sprintf(`{"execution_payload_included":false,"execution_payload_value":%q,"data":`, reported)), block...), '}'))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(!ssz), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	return service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include, BuilderConfig: config})
}

func TestEPBSProposalUnknownBuilderBelowMinimum(t *testing.T) {
	_, err := requestEPBSBid(t, gloas.ExecutionPayloadBid{Value: 1}, &gloas.BuilderConfig{MinBid: 10}, "https://unknown.example")
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "execution payload bid below minimum")
}

func TestEPBSProposalP2PPaymentBelowMinimum(t *testing.T) {
	_, err := requestEPBSBid(t, gloas.ExecutionPayloadBid{Value: 1, ExecutionPayment: 1}, &gloas.BuilderConfig{MinBid: 10})
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "execution payload bid below minimum")
}

func TestEPBSProposalSelfBuildRetainsValue(t *testing.T) {
	response, err := requestEPBSBid(t, gloas.ExecutionPayloadBid{BuilderIndex: gloas.BuilderIndex(^uint64(0))}, &gloas.BuilderConfig{MinBid: 10})
	require.NoError(t, err)
	require.Equal(t, "42", response.Data.Value().String())
	require.Equal(t, "42", response.Data.ExecutionValue.String())
}

func TestEPBSProposalWithheldValueRemovedFromMetadata(t *testing.T) {
	response, err := requestEPBSBid(t, gloas.ExecutionPayloadBid{Value: 10}, &gloas.BuilderConfig{MinBid: 10}, "https://unknown.example")
	require.NoError(t, err)
	require.Nil(t, response.Data.ExecutionValue)
	for key := range response.Metadata {
		require.False(t, strings.EqualFold("Eth-Execution-Payload-Value", key))
		require.False(t, strings.EqualFold("execution_payload_value", key))
	}
	require.Contains(t, response.Metadata, "execution_payload_included")
}

func TestEPBSProposalBidPolicies(t *testing.T) {
	entry := func(url string, floor, paymentCap phase0.Gwei) *gloas.BuilderEntry {
		builder := validEPBSBuilderConfig().Builders[0]
		builder.URL = []byte(url)
		builder.MinBid = floor
		builder.MaxExecutionPayment = paymentCap
		return builder
	}
	const first = "https://first.example"
	const second = "https://second.example"
	tests := []struct {
		name     string
		config   *gloas.BuilderConfig
		bid      gloas.ExecutionPayloadBid
		urls     []string
		reject   bool
		withheld bool
	}{
		{name: "duplicate below floor", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry(first, 0, 0), entry(first, 0, 0)}}, bid: gloas.ExecutionPayloadBid{Value: 9}, urls: []string{first}, reject: true},
		{name: "repeated headers below floor", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry(first, 0, 0), entry(second, 0, 0)}}, bid: gloas.ExecutionPayloadBid{Value: 9}, urls: []string{first, second}, reject: true},
		{name: "joined URL even if configured", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry(first+";"+second, 0, 0)}}, bid: gloas.ExecutionPayloadBid{Value: 9}, urls: []string{first, second}, reject: true},
		{name: "unknown uses largest cap", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry(first, 0, 1), entry(second, 0, 4)}}, bid: gloas.ExecutionPayloadBid{Value: 6, ExecutionPayment: 4}, urls: []string{"https://unknown.example"}, withheld: true},
		{name: "unknown caps payment", config: &gloas.BuilderConfig{MinBid: 11, Builders: []*gloas.BuilderEntry{entry(first, 0, 1), entry(second, 0, 4)}}, bid: gloas.ExecutionPayloadBid{Value: 6, ExecutionPayment: 8}, urls: []string{"https://unknown.example"}, reject: true},
		{name: "p2p uses largest cap", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry(first, 0, 1), entry(second, 0, 4)}}, bid: gloas.ExecutionPayloadBid{Value: 6, ExecutionPayment: 4}, withheld: true},
		{name: "p2p caps payment", config: &gloas.BuilderConfig{MinBid: 11, Builders: []*gloas.BuilderEntry{entry(first, 0, 1), entry(second, 0, 4)}}, bid: gloas.ExecutionPayloadBid{Value: 6, ExecutionPayment: 8}, reject: true},
		{name: "direct uses entry floor and cap", config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{entry(first, 99, 0), entry(second, 10, 4)}}, bid: gloas.ExecutionPayloadBid{Value: 6, ExecutionPayment: 4}, urls: []string{second}},
		{name: "direct does not use other cap", config: &gloas.BuilderConfig{MinBid: 0, Builders: []*gloas.BuilderEntry{entry(first, 0, 9), entry(second, 10, 1)}}, bid: gloas.ExecutionPayloadBid{Value: 6, ExecutionPayment: 4}, urls: []string{second}, reject: true},
		{name: "p2p without payment verifies value", config: &gloas.BuilderConfig{MinBid: 6}, bid: gloas.ExecutionPayloadBid{Value: 6}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := requestEPBSBid(t, test.bid, test.config, test.urls...)
			if test.reject {
				require.ErrorIs(t, err, client.ErrInconsistentResult)
				require.ErrorContains(t, err, "below minimum")
				return
			}
			require.NoError(t, err)
			if test.withheld {
				require.Nil(t, response.Data.ExecutionValue)
				return
			}
			require.NotNil(t, response.Data.ExecutionValue)
			require.Contains(t, response.Metadata, "execution_payload_value")
		})
	}
}

func TestEPBSProposalSSZWithheldValueRemovedFromMetadata(t *testing.T) {
	response, err := requestEPBSBidFormat(t, gloas.ExecutionPayloadBid{Value: 10, ExecutionPayment: 1}, &gloas.BuilderConfig{MinBid: 10}, true)
	require.NoError(t, err)
	require.Nil(t, response.Data.ExecutionValue)
	for key := range response.Metadata {
		require.False(t, strings.EqualFold("Eth-Execution-Payload-Value", key))
	}
	require.Contains(t, response.Metadata, "Eth-Execution-Payload-Included")
}

func TestEPBSProposalSelfBuildRejectsNonzeroBid(t *testing.T) {
	_, err := requestEPBSBid(t, gloas.ExecutionPayloadBid{BuilderIndex: gloas.BuilderIndex(^uint64(0)), Value: 1}, &gloas.BuilderConfig{})
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "self-built execution payload bid has non-zero value")
}
