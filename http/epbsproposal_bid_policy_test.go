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

func TestEPBSProposalUnmatchedBuilderStillEnforcesFloor(t *testing.T) {
	_, err := requestBidProposal(t, bidCase{value: 1, urls: []string{"https://unmatched.example"}, config: &gloas.BuilderConfig{MinBid: 1_000_000_000}})
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "below minimum")
}

func TestEPBSProposalP2PPaymentStillEnforcesFloor(t *testing.T) {
	_, err := requestBidProposal(t, bidCase{value: 1, payment: 1, config: &gloas.BuilderConfig{MinBid: 1_000_000_000}})
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "below minimum")
}

func TestEPBSProposalSelfBuildRetainsValue(t *testing.T) {
	response, err := requestBidProposal(t, bidCase{index: gloas.BuilderIndex(^uint64(0)), reported: "42", config: &gloas.BuilderConfig{}})
	require.NoError(t, err)
	require.Equal(t, big.NewInt(42), response.Data.ExecutionValue)
}

func TestEPBSProposalWithheldValueRemovedFromMetadata(t *testing.T) {
	response, err := requestBidProposal(t, bidCase{value: 10, urls: []string{"https://unknown.example"}, reported: "10000000000", config: &gloas.BuilderConfig{MinBid: 10}})
	require.NoError(t, err)
	require.Nil(t, response.Data.ExecutionValue)
	require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
	require.NotContains(t, response.Metadata, "execution_payload_value")
	require.Contains(t, response.Metadata, "version")
}

type bidCase struct {
	value, payment phase0.Gwei
	index          gloas.BuilderIndex
	urls           []string
	reported       string
	config         *gloas.BuilderConfig
	ssz            bool
}

func requestBidProposal(t *testing.T, c bidCase) (*api.Response[*api.VersionedEPBSProposal], error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mockService, err := mock.New(ctx)
	require.NoError(t, err)
	include := false
	proposal, err := mockService.EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include})
	require.NoError(t, err)
	proposal.Data.Gloas.Body.SignedExecutionPayloadBid.Message.Value = c.value
	proposal.Data.Gloas.Body.SignedExecutionPayloadBid.Message.ExecutionPayment = c.payment
	proposal.Data.Gloas.Body.SignedExecutionPayloadBid.Message.BuilderIndex = c.index
	var block []byte
	if c.ssz {
		block, err = proposal.Data.Gloas.MarshalSSZ()
	} else {
		block, err = json.Marshal(proposal.Data.Gloas)
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
			if c.reported != "" {
				w.Header().Set("Eth-Execution-Payload-Value", c.reported)
			}
			for _, url := range c.urls {
				w.Header().Add("Eth-Builder-Url", url)
			}
			if c.ssz {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Eth-Execution-Payload-Included", "false")
				_, _ = w.Write(block)
			} else {
				_, _ = w.Write(append([]byte(`{"version":"gloas","execution_payload_value":"10000000000","execution_payload_included":false,"data":`), append(block, '}')...))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(!c.ssz), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	return service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
		Slot: 123, IncludePayload: &include, BuilderConfig: c.config,
	})
}

func TestEPBSProposalJoinedBuilderURLIsUnattributed(t *testing.T) {
	config := validEPBSBuilderConfig()
	config.MinBid = 10
	config.Builders[0].URL = []byte("https://first.example;https://second.example")
	_, err := requestBidProposal(t, bidCase{value: 1, urls: []string{"https://first.example", "https://second.example"}, config: config})
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "below minimum")
}

func TestEPBSProposalBidPolicyBoundaries(t *testing.T) {
	entry := func(url string, floor, paymentCap phase0.Gwei) *gloas.BuilderEntry {
		builder := *validEPBSBuilderConfig().Builders[0]
		builder.URL = []byte(url)
		builder.MinBid = floor
		builder.MaxExecutionPayment = paymentCap
		return &builder
	}
	tests := []struct {
		name     string
		bid      bidCase
		rejected bool
		withheld bool
	}{
		{name: "DuplicateURLBelowFloor", bid: bidCase{value: 1, urls: []string{"https://a.example"}, config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 0), entry("https://a.example", 0, 0)}}}, rejected: true},
		{name: "DuplicateURLAtFloor", bid: bidCase{value: 10, urls: []string{"https://a.example"}, reported: "10000000000", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 0), entry("https://a.example", 0, 0)}}}, withheld: true},
		{name: "UnknownAtFloor", bid: bidCase{value: 10, urls: []string{"https://unknown.example"}, reported: "10000000000", config: &gloas.BuilderConfig{MinBid: 10}}, withheld: true},
		{name: "UnattributedMaximumCapAllowsFloor", bid: bidCase{value: 5, payment: 5, reported: "10000000000", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://b.example", 0, 5)}}}, withheld: true},
		{name: "UnattributedMaximumCapDoesNotDependOnOrder", bid: bidCase{value: 5, payment: 5, reported: "10000000000", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 5), entry("https://b.example", 0, 1)}}}, withheld: true},
		{name: "UnattributedCapLimitsPayment", bid: bidCase{value: 5, payment: 100, config: &gloas.BuilderConfig{MinBid: 11, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://b.example", 0, 5)}}}, rejected: true},
		{name: "DirectUsesEntryFloorBelowGlobal", bid: bidCase{value: 5, urls: []string{"https://b.example"}, reported: "5000000000", config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{entry("https://a.example", 99, 0), entry("https://b.example", 5, 0)}}}},
		{name: "DirectUsesEntryFloorAboveGlobal", bid: bidCase{value: 5, urls: []string{"https://b.example"}, config: &gloas.BuilderConfig{MinBid: 1, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 0), entry("https://b.example", 6, 0)}}}, rejected: true},
		{name: "DirectUsesResolvedCap", bid: bidCase{value: 5, payment: 5, urls: []string{"https://b.example"}, reported: "10000000000", config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1), entry("https://b.example", 10, 5)}}}},
		{name: "DirectDoesNotUseOtherCap", bid: bidCase{value: 5, payment: 5, urls: []string{"https://b.example"}, config: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 5), entry("https://b.example", 10, 1)}}}, rejected: true},
		{name: "P2PZeroPaymentIsVerifiable", bid: bidCase{value: 10, reported: "10000000000", config: &gloas.BuilderConfig{MinBid: 10}}},
		{name: "P2PPaymentWithoutCapRejected", bid: bidCase{value: 5, payment: 5, config: &gloas.BuilderConfig{MinBid: 10}}, rejected: true},
		{name: "UnattributedSumDoesNotOverflow", bid: bidCase{value: phase0.Gwei(^uint64(0)), payment: 1, config: &gloas.BuilderConfig{MinBid: phase0.Gwei(^uint64(0)), Builders: []*gloas.BuilderEntry{entry("https://a.example", 0, 1)}}}, withheld: true},
	}
	for _, test := range tests {
		for _, ssz := range []bool{false, true} {
			name := test.name + "/JSON"
			if ssz {
				name = test.name + "/SSZ"
			}
			t.Run(name, func(t *testing.T) {
				bid := test.bid
				bid.ssz = ssz
				response, err := requestBidProposal(t, bid)
				if test.rejected {
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
					require.NotNil(t, response.Data.ExecutionValue)
					require.Equal(t, test.bid.reported, response.Metadata["Eth-Execution-Payload-Value"])
				}
			})
		}
	}
}

func TestEPBSProposalSelfBuildInBothFormats(t *testing.T) {
	for _, ssz := range []bool{false, true} {
		name := "JSON"
		if ssz {
			name = "SSZ"
		}
		t.Run(name, func(t *testing.T) {
			response, err := requestBidProposal(t, bidCase{index: gloas.BuilderIndex(^uint64(0)), reported: "42", config: &gloas.BuilderConfig{}, ssz: ssz})
			require.NoError(t, err)
			require.Equal(t, big.NewInt(42), response.Data.Value())
			require.Equal(t, "42", response.Metadata["Eth-Execution-Payload-Value"])
			_, err = requestBidProposal(t, bidCase{index: gloas.BuilderIndex(^uint64(0)), value: 1, config: &gloas.BuilderConfig{}, ssz: ssz})
			require.ErrorIs(t, err, client.ErrInconsistentResult)
		})
	}
}
