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
	"strconv"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func bidPolicyResponse(t *testing.T, block *gloas.BeaconBlock, config *gloas.BuilderConfig, urls []string, encoding string) (*api.Response[*api.VersionedEPBSProposal], error) {
	t.Helper()
	data, err := json.Marshal(block)
	require.NoError(t, err)
	ssz, err := block.MarshalSSZ()
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			fmt.Fprint(w, `{"data":{"version":"test"}}`)
		case "/eth/v1/node/syncing":
			fmt.Fprint(w, `{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`)
		case "/eth/v4/validator/blocks/60300":
			w.Header().Set("Eth-Consensus-Version", "gloas")
			jsonValue := strconv.FormatInt(9_000_000_000, 10)
			if encoding == "json-invalid" {
				jsonValue = "bad"
			}
			if encoding != "json-body" && encoding != "json-invalid" {
				w.Header().Set("Eth-Execution-Payload-Value", "9000000000")
			}
			w.Header().Set("Eth-Consensus-Block-Value", "3")
			for _, url := range urls {
				w.Header().Add("Eth-Builder-Url", url)
			}
			if encoding == "ssz" {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Eth-Execution-Payload-Included", "false")
				_, _ = w.Write(ssz)
			} else {
				fmt.Fprintf(w, `{"version":"gloas","execution_payload_value":"%s","execution_payload_included":false,"data":%s}`, jsonValue, data)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := New(ctx, WithAddress(server.URL), WithEnforceJSON(true), WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	include := false
	return service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
		Slot: block.Slot, RandaoReveal: block.Body.RANDAOReveal, BuilderConfig: config, IncludePayload: &include,
	})
}

func TestEPBSProposalBidBelowMinimum(t *testing.T) {
	for _, test := range []struct {
		name    string
		urls    []string
		payment phase0.Gwei
	}{
		{name: "UnmatchedBuilder", urls: []string{"https://unknown.example"}},
		{name: "P2PPayment", payment: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			block := validEPBSBeaconBlock()
			block.Body.SignedExecutionPayloadBid.Message.Value = 1
			block.Body.SignedExecutionPayloadBid.Message.ExecutionPayment = test.payment
			_, err := bidPolicyResponse(t, block, &gloas.BuilderConfig{MinBid: 1_000_000_000}, test.urls, "json")
			require.ErrorIs(t, err, client.ErrInconsistentResult)
			require.ErrorContains(t, err, "execution payload bid below minimum")
		})
	}
}

func TestEPBSProposalSelfBuildValue(t *testing.T) {
	for _, encoding := range []string{"json", "ssz", "json-body"} {
		t.Run(encoding, func(t *testing.T) {
			block := validEPBSBeaconBlock()
			block.Body.SignedExecutionPayloadBid.Message.BuilderIndex = staticBuilderIndexSelfBuild
			response, err := bidPolicyResponse(t, block, &gloas.BuilderConfig{MinBid: 100}, nil, encoding)
			require.NoError(t, err)
			require.Equal(t, big.NewInt(9_000_000_000), response.Data.ExecutionValue)
			require.Equal(t, big.NewInt(9_000_000_003), response.Data.Value())
		})
	}
}

func TestEPBSProposalWithheldValueNotInMetadata(t *testing.T) {
	for _, encoding := range []string{"json", "ssz"} {
		t.Run(encoding, func(t *testing.T) {
			block := validEPBSBeaconBlock()
			block.Body.SignedExecutionPayloadBid.Message.Value = 10
			response, err := bidPolicyResponse(t, block, &gloas.BuilderConfig{MinBid: 10}, []string{"https://unknown.example"}, encoding)
			require.NoError(t, err)
			require.Nil(t, response.Data.ExecutionValue)
			require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
			require.NotContains(t, response.Metadata, "execution_payload_value")
			require.Equal(t, "3", response.Metadata["Eth-Consensus-Block-Value"])
		})
	}
}

func TestEPBSProposalBidPolicy(t *testing.T) {
	entry := func(url string, paymentCap, floor phase0.Gwei) *gloas.BuilderEntry {
		return &gloas.BuilderEntry{
			URL: []byte(url), MaxExecutionPayment: paymentCap, MinBid: floor,
			Auth: &gloas.SignedBuilderRequestAuth{Message: &gloas.BuilderRequestAuth{Slot: 60300, Data: []byte{1}}},
		}
	}
	const a = "https://a.example"
	const b = "https://b.example"
	for _, test := range []struct {
		name     string
		urls     []string
		builders []*gloas.BuilderEntry
		floor    phase0.Gwei
		payment  phase0.Gwei
		rejected bool
		withheld bool
	}{
		{name: "DuplicateURLBelowFloor", urls: []string{a}, builders: []*gloas.BuilderEntry{entry(a, 1, 0), entry(a, 2, 0)}, floor: 12, payment: 4, rejected: true},
		{name: "JoinedURLsBelowFloor", urls: []string{a, b}, builders: []*gloas.BuilderEntry{entry(a, 1, 0), entry(b, 2, 0)}, floor: 12, payment: 4, rejected: true},
		{name: "JoinedURLsMatchingEntry", urls: []string{a, b}, builders: []*gloas.BuilderEntry{entry(a+";"+b, 2, 0)}, floor: 12, payment: 4, rejected: true},
		{name: "UnmatchedMaxCapMeetsFloor", urls: []string{"https://other.example"}, builders: []*gloas.BuilderEntry{entry(a, 1, 0), entry(b, 3, 0)}, floor: 12, payment: 4, withheld: true},
		{name: "P2PMaxCapMeetsFloor", builders: []*gloas.BuilderEntry{entry(a, 1, 0), entry(b, 3, 0)}, floor: 12, payment: 4, withheld: true},
		{name: "P2PMaxCapBelowFloor", builders: []*gloas.BuilderEntry{entry(a, 1, 0), entry(b, 2, 0)}, floor: 12, payment: 4, rejected: true},
		{name: "AttributedUsesEntryFloorAndCap", urls: []string{b}, builders: []*gloas.BuilderEntry{entry(a, 10, 99), entry(b, 2, 11)}, floor: 100, payment: 4},
		{name: "AttributedCapBelowEntryFloor", urls: []string{b}, builders: []*gloas.BuilderEntry{entry(a, 10, 0), entry(b, 2, 12)}, floor: 0, payment: 4, rejected: true},
		{name: "P2PZeroPaymentVerified", floor: 9},
	} {
		t.Run(test.name, func(t *testing.T) {
			block := validEPBSBeaconBlock()
			block.Body.SignedExecutionPayloadBid.Message.Value = 9
			block.Body.SignedExecutionPayloadBid.Message.ExecutionPayment = test.payment
			response, err := bidPolicyResponse(t, block, &gloas.BuilderConfig{MinBid: test.floor, Builders: test.builders}, test.urls, "json")
			if test.rejected {
				require.ErrorIs(t, err, client.ErrInconsistentResult)
				require.ErrorContains(t, err, "execution payload bid below minimum")
				return
			}
			require.NoError(t, err)
			if test.withheld {
				require.Nil(t, response.Data.ExecutionValue)
			} else {
				require.Equal(t, big.NewInt(9_000_000_000), response.Data.ExecutionValue)
			}
		})
	}
}

func TestEPBSProposalRejectsInvalidJSONExecutionValue(t *testing.T) {
	block := validEPBSBeaconBlock()
	block.Body.SignedExecutionPayloadBid.Message.BuilderIndex = staticBuilderIndexSelfBuild
	_, err := bidPolicyResponse(t, block, &gloas.BuilderConfig{}, nil, "json-invalid")
	require.ErrorContains(t, err, "not a valid integer")
}
