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

func proposalFromTestNode(t *testing.T, bid gloas.ExecutionPayloadBid, config *gloas.BuilderConfig,
	builderURLs []string, jsonResponse bool,
) (*api.Response[*api.VersionedEPBSProposal], error) {
	t.Helper()
	ctx := context.Background()
	mockService, err := mock.New(ctx)
	require.NoError(t, err)
	include := false
	sample, err := mockService.EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include})
	require.NoError(t, err)
	sample.Data.Gloas.Body.SignedExecutionPayloadBid.Message = &bid
	var block []byte
	if jsonResponse {
		block, err = json.Marshal(sample.Data.Gloas)
	} else {
		block, err = sample.Data.Gloas.MarshalSSZ()
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
			w.Header().Set("Eth-Execution-Payload-Included", "false")
			w.Header().Set("Eth-Execution-Payload-Value", fmt.Sprint(7*1_000_000_000))
			w.Header().Set("Eth-Consensus-Block-Value", "3")
			for _, url := range builderURLs {
				w.Header().Add("Eth-Builder-Url", url)
			}
			if jsonResponse {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"version":"gloas","execution_payload_included":false,"execution_payload_value":"%d","data":%s}`, 7*1_000_000_000, block)
			} else {
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write(block)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(jsonResponse), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	return service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, BuilderConfig: config, IncludePayload: &include})
}

func TestEPBSProposalUnattributedBelowMinimum(t *testing.T) {
	for _, test := range []struct {
		name    string
		urls    []string
		payment phase0.Gwei
	}{
		{name: "UnknownBuilder", urls: []string{"https://unknown.example"}},
		{name: "P2PPayment", payment: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := proposalFromTestNode(t, gloas.ExecutionPayloadBid{Value: 1, ExecutionPayment: test.payment}, &gloas.BuilderConfig{MinBid: 1_000_000_000}, test.urls, true)
			require.ErrorIs(t, err, client.ErrInconsistentResult)
			require.ErrorContains(t, err, "below minimum")
		})
	}
}

func TestEPBSProposalSelfBuildKeepsValue(t *testing.T) {
	response, err := proposalFromTestNode(t, gloas.ExecutionPayloadBid{BuilderIndex: gloas.BuilderIndex(^uint64(0))}, &gloas.BuilderConfig{MinBid: 100}, nil, true)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(7*1_000_000_000), response.Data.ExecutionValue)
	require.Equal(t, big.NewInt(7*1_000_000_000+3), response.Data.Value())
}

func TestEPBSProposalWithheldValueAbsentFromMetadata(t *testing.T) {
	for _, jsonResponse := range []bool{false, true} {
		t.Run(fmt.Sprint("json=", jsonResponse), func(t *testing.T) {
			response, err := proposalFromTestNode(t, gloas.ExecutionPayloadBid{Value: 10}, &gloas.BuilderConfig{MinBid: 10}, []string{"https://unknown.example"}, jsonResponse)
			require.NoError(t, err)
			require.Nil(t, response.Data.ExecutionValue)
			require.Equal(t, big.NewInt(3), response.Data.Value())
			for key := range response.Metadata {
				require.False(t, strings.EqualFold(key, "Eth-Execution-Payload-Value"))
				require.False(t, strings.EqualFold(key, "execution_payload_value"))
			}
			require.Equal(t, "3", response.Metadata["Eth-Consensus-Block-Value"])
		})
	}
}

func TestEPBSProposalBidPolicy(t *testing.T) {
	entry := func(url string, floor, paymentCap phase0.Gwei) *gloas.BuilderEntry {
		return &gloas.BuilderEntry{
			URL: []byte(url), MinBid: floor, MaxExecutionPayment: paymentCap,
			Auth: &gloas.SignedBuilderRequestAuth{Message: &gloas.BuilderRequestAuth{Slot: 123, Data: []byte{1}}},
		}
	}
	const a = "https://a.example"
	const b = "https://b.example"
	for _, test := range []struct {
		name     string
		config   *gloas.BuilderConfig
		urls     []string
		bid      gloas.ExecutionPayloadBid
		reject   bool
		withhold bool
	}{
		{name: "UnmatchedBelowFloor", config: &gloas.BuilderConfig{MinBid: 8}, urls: []string{a}, bid: gloas.ExecutionPayloadBid{Value: 7}, reject: true},
		{name: "DuplicateBelowFloor", config: &gloas.BuilderConfig{MinBid: 8, Builders: []*gloas.BuilderEntry{entry(a, 0, 0), entry(a, 0, 0)}}, urls: []string{a}, bid: gloas.ExecutionPayloadBid{Value: 7}, reject: true},
		{name: "JoinedBelowFloor", config: &gloas.BuilderConfig{MinBid: 8, Builders: []*gloas.BuilderEntry{entry(a, 0, 0), entry(b, 0, 0), entry(a+";"+b, 0, 0)}}, urls: []string{a, b}, bid: gloas.ExecutionPayloadBid{Value: 7}, reject: true},
		{name: "P2PPaymentBelowFloor", config: &gloas.BuilderConfig{MinBid: 8}, bid: gloas.ExecutionPayloadBid{Value: 7, ExecutionPayment: 1}, reject: true},
		{name: "UnattributedMaxCapAccepts", config: &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry(a, 0, 1), entry(b, 0, 3)}}, bid: gloas.ExecutionPayloadBid{Value: 7, ExecutionPayment: 4}, withhold: true},
		{name: "UnattributedMaxCapRejects", config: &gloas.BuilderConfig{MinBid: 11, Builders: []*gloas.BuilderEntry{entry(a, 0, 1), entry(b, 0, 3)}}, bid: gloas.ExecutionPayloadBid{Value: 7, ExecutionPayment: 4}, reject: true},
		{name: "AttributedUsesEntryFloor", config: &gloas.BuilderConfig{MinBid: 99, Builders: []*gloas.BuilderEntry{entry(a, 8, 1), entry(b, 10, 3)}}, urls: []string{b}, bid: gloas.ExecutionPayloadBid{Value: 7, ExecutionPayment: 3}},
		{name: "AttributedUsesEntryCap", config: &gloas.BuilderConfig{MinBid: 0, Builders: []*gloas.BuilderEntry{entry(a, 10, 1), entry(b, 0, 3)}}, urls: []string{a}, bid: gloas.ExecutionPayloadBid{Value: 7, ExecutionPayment: 3}, reject: true},
		{name: "AttributedBelowEntryFloor", config: &gloas.BuilderConfig{MinBid: 0, Builders: []*gloas.BuilderEntry{entry(a, 8, 0)}}, urls: []string{a}, bid: gloas.ExecutionPayloadBid{Value: 7}, reject: true},
		{name: "UnmatchedAboveFloor", config: &gloas.BuilderConfig{MinBid: 7}, urls: []string{a}, bid: gloas.ExecutionPayloadBid{Value: 7}, withhold: true},
		{name: "P2PWithoutPayment", config: &gloas.BuilderConfig{MinBid: 7}, bid: gloas.ExecutionPayloadBid{Value: 7}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := proposalFromTestNode(t, test.bid, test.config, test.urls, true)
			if test.reject {
				require.ErrorIs(t, err, client.ErrInconsistentResult)
				require.ErrorContains(t, err, "below minimum")
				return
			}
			require.NoError(t, err)
			if test.withhold {
				require.Nil(t, response.Data.ExecutionValue)
			} else {
				require.Equal(t, big.NewInt(7*1_000_000_000), response.Data.ExecutionValue)
			}
		})
	}
}
