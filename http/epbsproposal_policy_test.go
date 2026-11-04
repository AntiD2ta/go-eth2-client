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
	"strings"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestEPBSProposalBidPolicy(t *testing.T) {
	entry := func(url string, floor, paymentCap phase0.Gwei) *gloas.BuilderEntry {
		return &gloas.BuilderEntry{
			URL: []byte(url), MinBid: floor, MaxExecutionPayment: paymentCap,
			Auth: &gloas.SignedBuilderRequestAuth{Message: &gloas.BuilderRequestAuth{Slot: 60300, Data: []byte{1}}},
		}
	}
	const direct = "https://direct.example"
	const other = "https://other.example"
	tests := []struct {
		name                            string
		urls                            []string
		builders                        []*gloas.BuilderEntry
		floor, value, payment           phase0.Gwei
		builder                         gloas.BuilderIndex
		reported                        string
		ssz, included, reject, withheld bool
	}{
		{name: "UnmatchedBelow", urls: []string{other}, floor: 10, value: 1, reject: true},
		{name: "DuplicateBelow", urls: []string{direct}, builders: []*gloas.BuilderEntry{entry(direct, 0, 0), entry(direct, 0, 0)}, floor: 10, value: 1, reject: true},
		{name: "JoinedBelow", urls: []string{direct, other}, builders: []*gloas.BuilderEntry{entry(direct, 0, 0)}, floor: 10, value: 1, reject: true},
		{name: "JoinedMatchesEntryBelow", urls: []string{direct, other}, builders: []*gloas.BuilderEntry{entry(direct+";"+other, 0, 0)}, floor: 10, value: 1, reject: true},
		{name: "P2PPaymentBelow", floor: 10, value: 1, payment: 1, reject: true},
		{name: "UnknownMaximumCapPass", urls: []string{other}, builders: []*gloas.BuilderEntry{entry(direct, 0, 2), entry("https://third.example", 0, 5)}, floor: 15, value: 10, payment: 8, reported: "999", withheld: true},
		{name: "UnknownMaximumCapFail", urls: []string{other}, builders: []*gloas.BuilderEntry{entry(direct, 0, 2), entry("https://third.example", 0, 5)}, floor: 16, value: 10, payment: 8, reject: true},
		{name: "P2PPaymentMaximumCapPass", builders: []*gloas.BuilderEntry{entry(direct, 0, 5)}, floor: 15, value: 10, payment: 8, reported: "999", withheld: true},
		{name: "P2PPaymentNoBuildersFail", floor: 11, value: 10, payment: 8, reject: true},
		{name: "DirectUsesOwnFloorAndCap", urls: []string{direct}, builders: []*gloas.BuilderEntry{entry(other, 0, 1), entry(direct, 12, 3)}, floor: 100, value: 10, payment: 3, reported: "13000000000"},
		{name: "DirectDoesNotUseOtherCap", urls: []string{direct}, builders: []*gloas.BuilderEntry{entry(other, 0, 9), entry(direct, 14, 3)}, floor: 1, value: 10, payment: 9, reject: true},
		{name: "SelfBuilt", floor: 100, builder: gloas.BuilderIndex(^uint64(0)), reported: "123"},
		{name: "SelfBuiltWithPayload", floor: 100, builder: gloas.BuilderIndex(^uint64(0)), reported: "123", included: true},
		{name: "UnknownSSZMetadata", urls: []string{other}, floor: 1, value: 1, reported: "999", ssz: true, withheld: true},
		{name: "P2PZeroPaymentVerified", floor: 1, value: 1, reported: "1000000000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			block := validEPBSBeaconBlock()
			contents := validEPBSBlockContents()
			if test.included {
				block = contents.Block
			}
			bid := block.Body.SignedExecutionPayloadBid.Message
			bid.BuilderIndex, bid.Value, bid.ExecutionPayment = test.builder, test.value, test.payment
			if test.included {
				contents.ExecutionPayloadEnvelope.BuilderIndex = test.builder
				root, err := block.HashTreeRoot()
				require.NoError(t, err)
				contents.ExecutionPayloadEnvelope.BeaconBlockRoot = root
			}
			var data []byte
			var err error
			if test.ssz {
				data, err = block.MarshalSSZ()
			} else if test.included {
				data, err = json.Marshal(contents)
			} else {
				data, err = json.Marshal(block)
			}
			require.NoError(t, err)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/node/syncing":
					fmt.Fprint(w, `{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`)
				case "/eth/v1/node/version":
					fmt.Fprint(w, `{"data":{"version":"test"}}`)
				case "/eth/v4/validator/blocks/60300":
					w.Header().Set("Eth-Consensus-Version", "gloas")
					for _, url := range test.urls {
						w.Header().Add("Eth-Builder-Url", url)
					}
					if test.reported != "" {
						w.Header().Set("Eth-Execution-Payload-Value", test.reported)
					}
					if test.ssz {
						w.Header().Set("Content-Type", "application/octet-stream")
						w.Header().Set("Eth-Execution-Payload-Included", "false")
						_, _ = w.Write(data)
					} else {
						fmt.Fprintf(w, `{"execution_payload_included":%t,"execution_payload_value":"%s","data":%s}`, test.included, test.reported, data)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service, err := New(ctx, WithAddress(server.URL), WithAllowDelayedStart(true), WithEnforceJSON(!test.ssz), WithLogLevel(zerolog.Disabled))
			require.NoError(t, err)
			included := test.included
			response, err := service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
				Slot: block.Slot, RandaoReveal: block.Body.RANDAOReveal,
				BuilderConfig: &gloas.BuilderConfig{MinBid: test.floor, Builders: test.builders}, IncludePayload: &included,
			})
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
			} else if test.reported != "" {
				expected, ok := new(big.Int).SetString(test.reported, 10)
				require.True(t, ok)
				require.Equal(t, expected, response.Data.ExecutionValue)
			}
		})
	}
}
