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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestResponseLimitJSONToSSZRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"x":1}`))
	}))
	defer server.Close()
	base, address, err := parseAddress(server.URL)
	require.NoError(t, err)
	s := &Service{base: base, address: address.String(), client: server.Client(), timeout: time.Second}

	res, err := s.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ,
		map[string]string{"Accept": "application/octet-stream"}, responseLimits{ssz: 4, json: 8})
	require.NoError(t, err)
	require.Equal(t, `{"x":1}`, string(res.body))
}

type responseLimitTransport func(*http.Request) (*http.Response, error)

func (f responseLimitTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type responseLimitBody struct {
	io.Reader
	reads int
}

func (b *responseLimitBody) Read(p []byte) (int, error) {
	b.reads++
	return b.Reader.Read(p)
}

func (*responseLimitBody) Close() error { return nil }

func TestResponseLimitsByEncoding(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		length      int64
		limits      responseLimits
		wantError   string
		wantUnread  bool
		preferJSON  bool
	}{
		{name: "JSONFallback", contentType: "application/json; charset=utf-8", body: `1234567`, length: -1, limits: responseLimits{ssz: 4, json: 8}},
		{name: "SSZTooLarge", contentType: "application/octet-stream", body: `12345`, length: -1, limits: responseLimits{ssz: 4, json: 8}, wantError: "response body exceeds 4 bytes"},
		{name: "SSZToJSONRequest", contentType: "application/octet-stream", body: `12345`, length: -1, limits: responseLimits{ssz: 4, json: 8}, wantError: "response body exceeds 4 bytes", preferJSON: true},
		{name: "MissingContentType", body: `1234567`, length: -1, limits: responseLimits{ssz: 4, json: 8}},
		{name: "UnknownContentType", contentType: "text/plain", body: `1234567`, length: -1, limits: responseLimits{ssz: 4, json: 8}},
		{name: "ExactSSZ", contentType: "application/octet-stream", body: `1234`, length: 4, limits: responseLimits{ssz: 4, json: 8}},
		{name: "ExactJSON", contentType: "application/json", body: `12345678`, length: 8, limits: responseLimits{ssz: 4, json: 8}},
		{name: "JSONTooLarge", contentType: "application/json", body: `123456789`, length: -1, limits: responseLimits{ssz: 4, json: 8}, wantError: "response body exceeds 8 bytes"},
		{name: "DeclaredSSZTooLarge", contentType: "application/octet-stream", body: `1`, length: 5, limits: responseLimits{ssz: 4, json: 8}, wantError: "response body exceeds 4 bytes", wantUnread: true},
		{name: "DeclaredJSONTooLarge", contentType: "application/json", body: `1`, length: 9, limits: responseLimits{ssz: 4, json: 8}, wantError: "response body exceeds 8 bytes", wantUnread: true},
		{name: "DeclaredMissingContentTypeTooLarge", body: `1`, length: 9, limits: responseLimits{ssz: 4, json: 8}, wantError: "response body exceeds 8 bytes", wantUnread: true},
		{name: "UnlimitedSSZ", contentType: "application/octet-stream", body: `123456789`, length: 9, limits: responseLimits{json: 4}},
		{name: "UnlimitedJSON", contentType: "application/json", body: `123456789`, length: 9, limits: responseLimits{ssz: 4}},
	}

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				body := &responseLimitBody{Reader: strings.NewReader(test.body)}
				headers := http.Header{"Eth-Consensus-Version": {"gloas"}}
				if test.contentType != "" {
					headers.Set("Content-Type", test.contentType)
				}
				base, address, err := parseAddress("http://localhost")
				require.NoError(t, err)
				s := &Service{base: base, address: address.String(), timeout: time.Second,
					client: &http.Client{Transport: responseLimitTransport(func(r *http.Request) (*http.Response, error) {
						require.Equal(t, method, r.Method)
						if test.preferJSON {
							require.Equal(t, "application/json", r.Header.Get("Accept"))
						} else {
							require.Contains(t, r.Header.Get("Accept"), "application/octet-stream")
						}
						return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: body, ContentLength: test.length}, nil
					})},
				}
				var res *httpResponse
				if method == http.MethodGet {
					res, err = s.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, !test.preferJSON, test.limits)
				} else {
					requestType := ContentTypeSSZ
					if test.preferJSON {
						requestType = ContentTypeJSON
					}
					res, err = s.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), requestType,
						map[string]string{"Accept": requestType.MediaType()}, test.limits)
				}
				if test.wantError != "" {
					require.ErrorContains(t, err, test.wantError)
					if test.wantUnread {
						require.Zero(t, body.reads)
					}
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(res.body))
				}
			})
		}
	}
}

func TestEPBSProposalJSONFallbackLimit(t *testing.T) {
	block := validEPBSBeaconBlock()
	responseBody, _ := epbsProposalJSONBody(t, false, block)
	body := &responseLimitBody{Reader: strings.NewReader(string(responseBody))}
	base, address, err := parseAddress("http://localhost")
	require.NoError(t, err)
	s := &Service{
		base: base, address: address.String(), timeout: time.Second, connectionActive: true, connectionSynced: true,
		client: &http.Client{Transport: responseLimitTransport(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "application/octet-stream", r.Header.Get("Accept"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{"Content-Type": {"application/json"}, "Eth-Consensus-Version": {"gloas"},
					"Eth-Execution-Payload-Included": {"false"}},
				Body: body, ContentLength: 256*1024*1024 + 1,
			}, nil
		})},
	}
	included := false
	response, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot: block.Slot, RandaoReveal: block.Body.RANDAOReveal,
		BuilderConfig: &gloas.BuilderConfig{}, IncludePayload: &included,
	})
	require.NoError(t, err)
	require.Equal(t, block.Slot, response.Data.Gloas.Slot)
}

func TestEPBSEndpointResponseLimits(t *testing.T) {
	included := false
	tests := []struct {
		name, method, path, contentType string
		length                          int64
		limit                           int
		call                            func(*Service) error
	}{
		{"ProposalSSZ", http.MethodPost, "/eth/v4/validator/blocks/1", "application/octet-stream", 256*1024*1024 + 1, 256 * 1024 * 1024, func(s *Service) error {
			_, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, BuilderConfig: &gloas.BuilderConfig{}, IncludePayload: &included})
			return err
		}},
		{"ProposalJSON", http.MethodPost, "/eth/v4/validator/blocks/1", "application/json", 512*1024*1024 + 1, 512 * 1024 * 1024, func(s *Service) error {
			_, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, BuilderConfig: &gloas.BuilderConfig{}, IncludePayload: &included})
			return err
		}},
		{"Envelope", http.MethodGet, "/eth/v1/validator/execution_payload_envelopes/1/", "application/octet-stream", 64*1024*1024 + 1, 64 * 1024 * 1024, func(s *Service) error {
			_, err := s.ExecutionPayloadEnvelope(context.Background(), &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})
			return err
		}},
		{"SignedEnvelope", http.MethodGet, "/eth/v1/beacon/execution_payload_envelopes/head", "application/json", 64*1024*1024 + 1, 64 * 1024 * 1024, func(s *Service) error {
			_, err := s.SignedExecutionPayloadEnvelope(context.Background(), &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{"AttestationData", http.MethodGet, "/eth/v1/validator/payload_attestation_data", "application/octet-stream", 64*1024*1024 + 1, 64 * 1024 * 1024, func(s *Service) error {
			_, err := s.PayloadAttestationData(context.Background(), &api.PayloadAttestationDataOpts{Slot: 1})
			return err
		}},
		{"AttestationPool", http.MethodGet, "/eth/v1/beacon/pool/payload_attestations", "application/json", 64*1024*1024 + 1, 64 * 1024 * 1024, func(s *Service) error {
			_, err := s.PayloadAttestationPool(context.Background(), &api.PayloadAttestationPoolOpts{})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &responseLimitBody{Reader: strings.NewReader("x")}
			base, address, err := parseAddress("http://localhost")
			require.NoError(t, err)
			s := &Service{base: base, address: address.String(), timeout: time.Second, connectionActive: true, connectionSynced: true,
				client: &http.Client{Transport: responseLimitTransport(func(r *http.Request) (*http.Response, error) {
					require.Equal(t, test.method, r.Method)
					require.True(t, strings.HasPrefix(r.URL.Path, test.path))
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {test.contentType}}, Body: body, ContentLength: test.length}, nil
				})},
			}
			err = test.call(s)
			require.ErrorContains(t, err, fmt.Sprintf("response body exceeds %d bytes", test.limit))
			require.Zero(t, body.reads)
		})
	}
}
