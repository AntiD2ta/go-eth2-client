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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func validEPBSBuilderConfig() *gloas.BuilderConfig {
	return &gloas.BuilderConfig{
		Builders: []*gloas.BuilderEntry{{
			URL: []byte("https://builder.example"),
			Auth: &gloas.SignedBuilderRequestAuth{
				Message:   &gloas.BuilderRequestAuth{Data: []byte{0x01}, Slot: 123},
				Signature: phase0.BLSSignature{0x02},
			},
			BuilderPubkeys: []phase0.BLSPubKey{},
		}},
	}
}

func TestEPBSProposalJSONRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
			require.NoError(t, err)
		case "/eth/v1/node/version":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"version":"test"}}`))
			require.NoError(t, err)
		case "/eth/v4/validator/blocks/123":
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			require.Equal(t, "application/json", r.Header.Get("Accept"))
			w.WriteHeader(http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service, err := eth2http.New(context.Background(),
		eth2http.WithAddress(server.URL),
		eth2http.WithAllowDelayedStart(true),
		eth2http.WithEnforceJSON(true),
		eth2http.WithLogLevel(zerolog.Disabled),
	)
	require.NoError(t, err)

	includePayload := true
	_, err = service.(client.EPBSProposalProvider).EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot:           123,
		BuilderConfig:  validEPBSBuilderConfig(),
		IncludePayload: &includePayload,
	})
	require.Error(t, err)
}

func TestEPBSProposalJSONResponseAcceptsBareBuilderWin(t *testing.T) {
	mockService, err := mock.New(context.Background())
	require.NoError(t, err)
	excludePayload := false
	mockResponse, err := mockService.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot:           123,
		IncludePayload: &excludePayload,
	})
	require.NoError(t, err)
	block, err := json.Marshal(mockResponse.Data.Gloas)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
			require.NoError(t, err)
		case "/eth/v1/node/version":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"version":"test"}}`))
			require.NoError(t, err)
		case "/eth/v4/validator/blocks/123":
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			require.Equal(t, "application/json", r.Header.Get("Accept"))
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Eth-Consensus-Version", "gloas")
			_, err := w.Write(append([]byte(`{"version":"gloas","execution_payload_included":false,"data":`), append(block, '}')...))
			require.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service, err := eth2http.New(context.Background(),
		eth2http.WithAddress(server.URL),
		eth2http.WithAllowDelayedStart(true),
		eth2http.WithEnforceJSON(true),
		eth2http.WithLogLevel(zerolog.Disabled),
	)
	require.NoError(t, err)

	includePayload := true
	response, err := service.(client.EPBSProposalProvider).EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot:           123,
		BuilderConfig:  validEPBSBuilderConfig(),
		IncludePayload: &includePayload,
	})
	require.NoError(t, err)
	require.False(t, response.Data.ExecutionPayloadIncluded)
	require.NotNil(t, response.Data.Gloas)
	require.Nil(t, response.Data.GloasContents)
}

func TestEPBSProposalRejectsNonUTF8BuilderURL(t *testing.T) {
	var blockRequests atomic.Uint64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
			require.NoError(t, err)
		case "/eth/v1/node/version":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"version":"test"}}`))
			require.NoError(t, err)
		case "/eth/v4/validator/blocks/123":
			blockRequests.Add(1)
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service, err := eth2http.New(context.Background(),
		eth2http.WithAddress(server.URL),
		eth2http.WithAllowDelayedStart(true),
		eth2http.WithLogLevel(zerolog.Disabled),
	)
	require.NoError(t, err)

	includePayload := true
	config := validEPBSBuilderConfig()
	config.Builders[0].URL = []byte{0xff}

	_, err = service.(client.EPBSProposalProvider).EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot:           123,
		BuilderConfig:  config,
		IncludePayload: &includePayload,
	})
	require.ErrorContains(t, err, "builder 0 has invalid URL")
	require.Zero(t, blockRequests.Load())
}

func TestEPBSProposalWithheldValueIsAbsentFromMetadata(t *testing.T) {
	for _, format := range []string{"JSON", "SSZ"} {
		t.Run(format, func(t *testing.T) {
			ctx := context.Background()
			mockService, err := mock.New(ctx)
			require.NoError(t, err)
			included := false
			mockResponse, err := mockService.EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &included})
			require.NoError(t, err)
			mockResponse.Data.Gloas.Body.SignedExecutionPayloadBid.Message.BuilderIndex = 1
			mockResponse.Data.Gloas.Body.SignedExecutionPayloadBid.Message.Value = 1
			var block []byte
			if format == "JSON" {
				block, err = json.Marshal(mockResponse.Data.Gloas)
			} else {
				block, err = mockResponse.Data.Gloas.MarshalSSZ()
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
					w.Header().Add("Eth-Builder-Url", "https://builder.example")
					w.Header().Add("Eth-Builder-Url", "https://unknown.example")
					w.Header().Set("Eth-Execution-Payload-Value", "123")
					if format == "JSON" {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write(append([]byte(`{"version":"gloas","execution_payload_included":false,"execution_payload_value":"123","data":`), append(block, '}')...))
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
			service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(format == "JSON"), eth2http.WithLogLevel(zerolog.Disabled))
			require.NoError(t, err)
			response, err := service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
				Slot: 123, BuilderConfig: validEPBSBuilderConfig(), IncludePayload: &included,
			})
			require.NoError(t, err)
			require.Nil(t, response.Data.ExecutionValue)
			require.NotContains(t, response.Metadata, "Eth-Execution-Payload-Value")
			require.NotContains(t, response.Metadata, "execution_payload_value")
			require.Equal(t, "https://builder.example;https://unknown.example", response.Metadata["Eth-Builder-Url"])

			config := validEPBSBuilderConfig()
			config.MinBid = 2
			_, err = service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
				Slot: 123, BuilderConfig: config, IncludePayload: &included,
			})
			require.ErrorIs(t, err, client.ErrInconsistentResult)
			require.ErrorContains(t, err, "execution payload bid below minimum")
		})
	}
}

func TestEPBSProposalSelfBuildUsesJSONExecutionValue(t *testing.T) {
	ctx := context.Background()
	mockService, err := mock.New(ctx)
	require.NoError(t, err)
	included := false
	mockResponse, err := mockService.EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, IncludePayload: &included})
	require.NoError(t, err)
	mockResponse.Data.Gloas.Body.SignedExecutionPayloadBid.Message.BuilderIndex = gloas.BuilderIndex(^uint64(0))
	mockResponse.Data.Gloas.Body.SignedExecutionPayloadBid.Message.Value = 0
	block, err := json.Marshal(mockResponse.Data.Gloas)
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
			_, _ = w.Write(append([]byte(`{"version":"gloas","execution_payload_included":false,"execution_payload_value":"123","data":`), append(block, '}')...))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithEnforceJSON(true), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	response, err := service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{
		Slot: 123, BuilderConfig: &gloas.BuilderConfig{}, IncludePayload: &included,
	})
	require.NoError(t, err)
	require.Equal(t, "123", response.Data.Value().String())
	require.Equal(t, "123", response.Metadata["execution_payload_value"])
}
