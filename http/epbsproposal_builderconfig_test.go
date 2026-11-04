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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

type responseLimitTransport func(*http.Request) (*http.Response, error)

func (f responseLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type responseLimitBody struct {
	read bool
}

func (b *responseLimitBody) Read([]byte) (int, error) {
	b.read = true
	return 0, errors.New("unexpected body read")
}

func (*responseLimitBody) Close() error { return nil }

func TestEPBSProposalRejectsOversizedDeclaredJSONBeforeReading(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tests := []struct {
		name, contentType, accept, expected string
		enforceJSON                         bool
		length                              int64
	}{
		{name: "JSONToSSZRequest", contentType: "application/json", accept: "application/octet-stream", length: 512*1024*1024 + 1, expected: "response body exceeds 536870912 bytes"},
		{name: "MissingContentType", accept: "application/octet-stream", length: 512*1024*1024 + 1, expected: "response body exceeds 536870912 bytes"},
		{name: "UnknownContentType", contentType: "text/plain", accept: "application/octet-stream", length: 512*1024*1024 + 1, expected: "response body exceeds 536870912 bytes"},
		{name: "SSZToJSONRequest", contentType: "application/octet-stream", accept: "application/json", enforceJSON: true, length: 256*1024*1024 + 1, expected: "response body exceeds 268435456 bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &responseLimitBody{}
			transport := responseLimitTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/eth/v4/validator/blocks/123" {
					return http.DefaultTransport.RoundTrip(req)
				}
				require.Equal(t, test.accept, req.Header.Get("Accept"))
				header := make(http.Header)
				if test.contentType != "" {
					header.Set("Content-Type", test.contentType)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.length, Body: body}, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithHTTPClient(&http.Client{Transport: transport}), eth2http.WithEnforceJSON(test.enforceJSON), eth2http.WithLogLevel(zerolog.Disabled))
			require.NoError(t, err)
			includePayload := true
			_, err = service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, BuilderConfig: validEPBSBuilderConfig(), IncludePayload: &includePayload})
			require.ErrorContains(t, err, test.expected)
			require.False(t, body.read)
		})
	}
}

func TestEPBSProposalSSZRequestAcceptsJSONResponseLimit(t *testing.T) {
	mockService, err := mock.New(context.Background())
	require.NoError(t, err)
	include := false
	proposal, err := mockService.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include})
	require.NoError(t, err)
	block, err := json.Marshal(proposal.Data.Gloas)
	require.NoError(t, err)
	body := append([]byte(`{"version":"gloas","execution_payload_included":false,"data":`), block...)
	body = append(body, '}')

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	transport := responseLimitTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/eth/v4/validator/blocks/123" {
			return http.DefaultTransport.RoundTrip(req)
		}
		require.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}, "Eth-Consensus-Version": {"gloas"}}, ContentLength: 256*1024*1024 + 1, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithHTTPClient(&http.Client{Transport: transport}), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	response, err := service.(client.EPBSProposalProvider).EPBSProposal(ctx, &api.EPBSProposalOpts{Slot: 123, BuilderConfig: validEPBSBuilderConfig(), IncludePayload: &include})
	require.NoError(t, err)
	require.Equal(t, phase0.Slot(123), response.Data.Gloas.Slot)
}
