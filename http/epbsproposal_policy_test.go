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
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestEPBSProposalUnmatchedBuilderCannotBypassMinimum(t *testing.T) {
	testEPBSProposalBelowMinimum(t, "https://unknown.example", 0)
}

func TestEPBSProposalP2PPaymentCannotBypassMinimum(t *testing.T) {
	testEPBSProposalBelowMinimum(t, "", 1)
}

func TestEPBSProposalSelfBuildRetainsExecutionValue(t *testing.T) {
	block := validEPBSBeaconBlock()
	block.Body.SignedExecutionPayloadBid.Message.BuilderIndex = staticBuilderIndexSelfBuild
	response, err := requestPolicyProposal(t, block, &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}, "")
	require.NoError(t, err)
	require.Equal(t, big.NewInt(1_000_000_000), response.Data.ExecutionValue)
	require.Equal(t, big.NewInt(1_000_000_000), response.Data.Value())
}

func TestEPBSProposalWithheldValueIsAbsentFromMetadata(t *testing.T) {
	block := validEPBSBeaconBlock()
	block.Body.SignedExecutionPayloadBid.Message.BuilderIndex = 1
	response, err := requestPolicyProposal(t, block, &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}, "https://unknown.example")
	require.NoError(t, err)
	require.Nil(t, response.Data.ExecutionValue)
	require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
	require.NotContains(t, response.Metadata, "execution_payload_value")
}

func TestEPBSProposalSSZWithheldValueIsAbsentFromMetadata(t *testing.T) {
	block := validEPBSBeaconBlock()
	block.Body.SignedExecutionPayloadBid.Message.BuilderIndex = 1
	response, err := requestPolicyProposal(t, block, &gloas.BuilderConfig{}, "https://unknown.example", true)
	require.NoError(t, err)
	require.Nil(t, response.Data.ExecutionValue)
	require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
}

func TestEPBSProposalJoinedBuilderHeadersAreUnattributed(t *testing.T) {
	block := validEPBSBeaconBlock()
	block.Body.SignedExecutionPayloadBid.Message.BuilderIndex = 1
	block.Body.SignedExecutionPayloadBid.Message.Value = 1
	entry := policyEntry("https://one.example;https://two.example", 0, 0, block.Slot)
	_, err := requestPolicyProposal(t, block, &gloas.BuilderConfig{MinBid: 10, Builders: []*gloas.BuilderEntry{entry}}, "https://one.example;https://two.example")
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "below minimum")
}

func policyEntry(url string, paymentCap, floor phase0.Gwei, slot phase0.Slot) *gloas.BuilderEntry {
	return &gloas.BuilderEntry{URL: []byte(url), MaxExecutionPayment: paymentCap, MinBid: floor,
		Auth:           &gloas.SignedBuilderRequestAuth{Message: &gloas.BuilderRequestAuth{Data: []byte{1}, Slot: slot}},
		BuilderPubkeys: []phase0.BLSPubKey{},
	}
}

func TestEPBSProposalBidPolicy(t *testing.T) {
	const slot = phase0.Slot(60300)
	direct := func(paymentCap, floor phase0.Gwei) *gloas.BuilderEntry {
		return policyEntry("https://direct.example", paymentCap, floor, slot)
	}
	other := func(paymentCap phase0.Gwei) *gloas.BuilderEntry {
		return policyEntry("https://other.example", paymentCap, 0, slot)
	}
	tests := []struct {
		name             string
		url              string
		floor, payment   phase0.Gwei
		entries          []*gloas.BuilderEntry
		reject, withheld bool
	}{
		{name: "Duplicate URL uses top level floor", url: "https://direct.example", floor: 2, entries: []*gloas.BuilderEntry{direct(0, 0), direct(0, 0)}, reject: true},
		{name: "Unknown URL uses largest cap", url: "https://unknown.example", floor: 5, payment: 5, entries: []*gloas.BuilderEntry{other(1), direct(4, 0)}, withheld: true},
		{name: "P2P payment uses largest cap", floor: 5, payment: 5, entries: []*gloas.BuilderEntry{other(1), direct(4, 0)}, withheld: true},
		{name: "Unknown URL cap limits payment", url: "https://unknown.example", floor: 6, payment: 5, entries: []*gloas.BuilderEntry{other(1), direct(4, 0)}, reject: true},
		{name: "P2P payment below floor", floor: 6, payment: 5, entries: []*gloas.BuilderEntry{other(1), direct(4, 0)}, reject: true},
		{name: "Direct entry floor overrides top level", url: "https://direct.example", floor: 50, payment: 2, entries: []*gloas.BuilderEntry{other(9), direct(2, 3)}},
		{name: "Direct entry floor enforced", url: "https://direct.example", floor: 0, payment: 2, entries: []*gloas.BuilderEntry{other(9), direct(2, 4)}, reject: true},
		{name: "Direct uses its own cap", url: "https://direct.example", floor: 0, payment: 8, entries: []*gloas.BuilderEntry{other(9), direct(2, 4)}, reject: true},
		{name: "No builders means zero payment cap", floor: 2, payment: 1, reject: true},
		{name: "No payment verifies P2P", floor: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			block := validEPBSBeaconBlock()
			bid := block.Body.SignedExecutionPayloadBid.Message
			bid.BuilderIndex, bid.Value, bid.ExecutionPayment = 1, 1, test.payment
			response, err := requestPolicyProposal(t, block, &gloas.BuilderConfig{MinBid: test.floor, Builders: test.entries}, test.url)
			if test.reject {
				require.ErrorIs(t, err, client.ErrInconsistentResult)
				require.ErrorContains(t, err, "below minimum")
				return
			}
			require.NoError(t, err)
			if test.withheld {
				require.Nil(t, response.Data.ExecutionValue)
				require.NotContains(t, response.Metadata, "execution_payload_value")
			} else {
				require.Equal(t, big.NewInt(1_000_000_000), response.Data.ExecutionValue)
			}
		})
	}
}

func testEPBSProposalBelowMinimum(t *testing.T, builderURL string, payment uint64) {
	t.Helper()
	block := validEPBSBeaconBlock()
	block.Body.SignedExecutionPayloadBid.Message.BuilderIndex = 1
	block.Body.SignedExecutionPayloadBid.Message.Value = 1
	block.Body.SignedExecutionPayloadBid.Message.ExecutionPayment = phase0.Gwei(payment)
	_, err := requestPolicyProposal(t, block, &gloas.BuilderConfig{MinBid: 1_000_000_000, Builders: []*gloas.BuilderEntry{}}, builderURL)
	require.ErrorIs(t, err, client.ErrInconsistentResult)
	require.ErrorContains(t, err, "below minimum")
}

func requestPolicyProposal(t *testing.T, block *gloas.BeaconBlock, config *gloas.BuilderConfig, builderURL string, useSSZ ...bool) (*api.Response[*api.VersionedEPBSProposal], error) {
	t.Helper()
	ssz := len(useSSZ) > 0 && useSSZ[0]
	var data []byte
	var err error
	if ssz {
		data, err = block.MarshalSSZ()
	} else {
		data, err = json.Marshal(block)
	}
	require.NoError(t, err)
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			fmt.Fprint(w, `{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`)
		case "/eth/v1/node/version":
			fmt.Fprint(w, `{"data":{"version":"test"}}`)
		case "/eth/v4/validator/blocks/60300":
			w.Header().Set("Eth-Consensus-Version", "gloas")
			w.Header().Set("Eth-Execution-Payload-Value", "1000000000")
			for _, value := range strings.Split(builderURL, ";") {
				if value != "" {
					w.Header().Add("Eth-Builder-Url", value)
				}
			}
			if ssz {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Eth-Execution-Payload-Included", "false")
				_, _ = w.Write(data)
			} else {
				fmt.Fprintf(w, `{"version":"gloas","execution_payload_value":"%d","execution_payload_included":false,"data":%s}`, 1_000_000_000, data)
			}
		default:
			stdhttp.NotFound(w, r)
		}
	}))
	defer server.Close()
	service, err := New(context.Background(), WithAddress(server.URL), WithAllowDelayedStart(true), WithEnforceJSON(!ssz))
	require.NoError(t, err)
	include := false
	return service.(client.EPBSProposalProvider).EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot: block.Slot, RandaoReveal: block.Body.RANDAOReveal, IncludePayload: &include,
		BuilderConfig: config,
	})
}
