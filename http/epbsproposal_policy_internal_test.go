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
	"github.com/stretchr/testify/require"
)

func policyProposal(t *testing.T, bid *gloas.ExecutionPayloadBid, config *gloas.BuilderConfig, useJSON bool, urls ...string) (*api.Response[*api.VersionedEPBSProposal], error) {
	t.Helper()
	block := validEPBSBeaconBlock()
	block.Body.SignedExecutionPayloadBid.Message = bid
	var body []byte
	if useJSON {
		body, _ = epbsProposalJSONBody(t, false, block)
	} else {
		var err error
		body, err = block.MarshalSSZ()
		require.NoError(t, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
		case "/eth/v4/validator/blocks/60300":
			if !useJSON {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Eth-Execution-Payload-Included", "false")
			}
			w.Header().Set("Eth-Consensus-Version", "gloas")
			value := fmt.Sprintf("%d", uint64(bid.Value)*1_000_000_000)
			if bid.BuilderIndex == staticBuilderIndexSelfBuild {
				value = "12345"
			}
			w.Header().Set("Eth-Execution-Payload-Value", value)
			for _, url := range urls {
				w.Header().Add("Eth-Builder-Url", url)
			}
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := New(ctx, WithAddress(server.URL), WithEnforceJSON(useJSON))
	require.NoError(t, err)
	include := false
	return service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
		Slot: block.Slot, RandaoReveal: block.Body.RANDAOReveal, IncludePayload: &include,
		BuilderConfig: config,
	})
}

func TestEPBSProposalUnattributedBidStillChecksMinimum(t *testing.T) {
	for _, test := range []struct {
		name, url string
		payment   phase0.Gwei
	}{
		{name: "UnmatchedBuilder", url: "https://unknown.example"},
		{name: "P2PPayment", payment: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			bid := &gloas.ExecutionPayloadBid{Value: 1, ExecutionPayment: test.payment}
			urls := []string{}
			if test.url != "" {
				urls = append(urls, test.url)
			}
			_, err := policyProposal(t, bid, &gloas.BuilderConfig{MinBid: 10}, true, urls...)
			require.ErrorIs(t, err, client.ErrInconsistentResult)
			require.ErrorContains(t, err, "below minimum")
		})
	}
}

func TestEPBSProposalSelfBuildKeepsValue(t *testing.T) {
	response, err := policyProposal(t, &gloas.ExecutionPayloadBid{BuilderIndex: staticBuilderIndexSelfBuild}, &gloas.BuilderConfig{MinBid: 10}, true)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(12345), response.Data.ExecutionValue)
	require.Equal(t, big.NewInt(12345), response.Data.Value())
}

func TestEPBSProposalWithheldValueIsNotExposedInMetadata(t *testing.T) {
	for _, test := range []struct {
		name string
		json bool
	}{{"JSON", true}, {"SSZ", false}} {
		t.Run(test.name, func(t *testing.T) {
			response, err := policyProposal(t, &gloas.ExecutionPayloadBid{Value: 10}, &gloas.BuilderConfig{MinBid: 10}, test.json, "https://unknown.example")
			require.NoError(t, err)
			require.Nil(t, response.Data.ExecutionValue)
			for key := range response.Metadata {
				require.False(t, strings.EqualFold(key, "Eth-Execution-Payload-Value"))
				require.False(t, strings.EqualFold(key, "execution_payload_value"))
			}
		})
	}
}

func policyEntry(url string, minBid, maxPayment phase0.Gwei) *gloas.BuilderEntry {
	return &gloas.BuilderEntry{
		URL: []byte(url), MinBid: minBid, MaxExecutionPayment: maxPayment,
		Auth: &gloas.SignedBuilderRequestAuth{Message: &gloas.BuilderRequestAuth{Slot: 60300, Data: []byte{1}}},
	}
}

func TestEPBSProposalJoinedBuilderHeadersAreUnattributed(t *testing.T) {
	config := &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{
		policyEntry("https://one.example;https://two.example", 0, 0),
	}}
	_, err := policyProposal(t, &gloas.ExecutionPayloadBid{Value: 1}, config, true, "https://one.example", "https://two.example")
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "below minimum")
}

func TestEPBSProposalBidPolicy(t *testing.T) {
	for _, test := range []struct {
		name      string
		config    *gloas.BuilderConfig
		urls      []string
		bid       gloas.ExecutionPayloadBid
		wantError bool
		withheld  bool
	}{
		{name: "DuplicateURLBelowFloor", config: &gloas.BuilderConfig{MinBid: 9, Builders: []*gloas.BuilderEntry{policyEntry("https://a.example", 0, 2), policyEntry("https://a.example", 0, 4)}}, urls: []string{"https://a.example"}, bid: gloas.ExecutionPayloadBid{Value: 4, ExecutionPayment: 4}, wantError: true},
		{name: "DuplicateURLUsesLargestCap", config: &gloas.BuilderConfig{MinBid: 8, Builders: []*gloas.BuilderEntry{policyEntry("https://a.example", 0, 2), policyEntry("https://a.example", 0, 4)}}, urls: []string{"https://a.example"}, bid: gloas.ExecutionPayloadBid{Value: 4, ExecutionPayment: 5}, withheld: true},
		{name: "P2PUsesLargestCap", config: &gloas.BuilderConfig{MinBid: 8, Builders: []*gloas.BuilderEntry{policyEntry("https://a.example", 0, 2), policyEntry("https://b.example", 0, 4)}}, bid: gloas.ExecutionPayloadBid{Value: 4, ExecutionPayment: 5}, withheld: true},
		{name: "UnmatchedUsesLargestCap", config: &gloas.BuilderConfig{MinBid: 8, Builders: []*gloas.BuilderEntry{policyEntry("https://a.example", 0, 2), policyEntry("https://b.example", 0, 4)}}, urls: []string{"https://unknown.example"}, bid: gloas.ExecutionPayloadBid{Value: 4, ExecutionPayment: 5}, withheld: true},
		{name: "DirectUsesEntryFloorAndCap", config: &gloas.BuilderConfig{MinBid: 100, Builders: []*gloas.BuilderEntry{policyEntry("https://a.example", 0, 20), policyEntry("https://b.example", 8, 4)}}, urls: []string{"https://b.example"}, bid: gloas.ExecutionPayloadBid{Value: 4, ExecutionPayment: 5}},
		{name: "DirectDoesNotUseOtherCap", config: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{policyEntry("https://a.example", 0, 20), policyEntry("https://b.example", 9, 4)}}, urls: []string{"https://b.example"}, bid: gloas.ExecutionPayloadBid{Value: 4, ExecutionPayment: 5}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := policyProposal(t, &test.bid, test.config, true, test.urls...)
			if test.wantError {
				require.ErrorIs(t, err, client.ErrInconsistentResult)
				require.ErrorContains(t, err, "below minimum")
				return
			}
			require.NoError(t, err)
			if test.withheld {
				require.Nil(t, response.Data.ExecutionValue)
				require.NotContains(t, response.Metadata, "execution_payload_value")
				require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
			} else {
				require.Equal(t, big.NewInt(4_000_000_000), response.Data.ExecutionValue)
			}
		})
	}
}
