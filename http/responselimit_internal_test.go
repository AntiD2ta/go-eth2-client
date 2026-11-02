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
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestResponseLimitJSONToSSZRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/octet-stream", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("12345"))
	}))
	defer server.Close()
	s := &Service{base: mustParseURL(server.URL), client: server.Client(), timeout: time.Second, log: zerolog.Nop()}
	response, err := s.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, responseLimits{ssz: 4, json: 5})
	require.NoError(t, err)
	require.Equal(t, "12345", string(response.body))
}

type responseLimitTransport func(*http.Request) (*http.Response, error)

func (f responseLimitTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type responseLimitBody struct{ read bool }

func (b *responseLimitBody) Read([]byte) (int, error) {
	b.read = true
	return 0, io.EOF
}

func (*responseLimitBody) Close() error { return nil }

func TestResponseLimitRejectsContentLengthBeforeRead(t *testing.T) {
	body := &responseLimitBody{}
	s := &Service{
		base: mustParseURL("http://example.com"), timeout: time.Second, log: zerolog.Nop(),
		client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: 6, Body: body}, nil
		})},
	}
	_, err := s.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, responseLimits{ssz: 4, json: 5})
	require.ErrorContains(t, err, "response body exceeds 5 bytes")
	require.False(t, body.read)
}

func TestResponseLimitsByEncoding(t *testing.T) {
	tests := []struct {
		name        string
		contentType []string
		limits      responseLimits
		body        string
		wantType    ContentType
		wantError   string
	}{
		{name: "JSONAtLimit", contentType: []string{"application/json; charset=utf-8"}, limits: responseLimits{ssz: 3, json: 5}, body: "12345", wantType: ContentTypeJSON},
		{name: "JSONOverLimit", contentType: []string{"application/json"}, limits: responseLimits{ssz: 3, json: 5}, body: "123456", wantError: "response body exceeds 5 bytes"},
		{name: "SSZAtLimit", contentType: []string{"application/octet-stream"}, limits: responseLimits{ssz: 3, json: 5}, body: "123", wantType: ContentTypeSSZ},
		{name: "SSZOverLimit", contentType: []string{"application/octet-stream"}, limits: responseLimits{ssz: 3, json: 5}, body: "1234", wantError: "response body exceeds 3 bytes"},
		{name: "MissingType", limits: responseLimits{ssz: 3, json: 5}, body: "12345", wantType: ContentTypeJSON},
		{name: "MissingTypeOverLimit", limits: responseLimits{ssz: 3, json: 5}, body: "123456", wantError: "response body exceeds 5 bytes"},
		{name: "UnknownType", contentType: []string{"text/plain"}, limits: responseLimits{ssz: 3, json: 5}, body: "12345", wantType: ContentTypeJSON},
		{name: "UnknownTypeOverLimit", contentType: []string{"text/plain"}, limits: responseLimits{ssz: 3, json: 5}, body: "123456", wantError: "response body exceeds 5 bytes"},
		{name: "MultipleTypes", contentType: []string{"application/octet-stream", "application/json"}, limits: responseLimits{ssz: 3, json: 5}, body: "12345", wantType: ContentTypeJSON},
		{name: "UnboundedJSON", contentType: []string{"application/json"}, limits: responseLimits{ssz: 3}, body: "123456", wantType: ContentTypeJSON},
		{name: "UnboundedSSZ", contentType: []string{"application/octet-stream"}, limits: responseLimits{json: 3}, body: "123456", wantType: ContentTypeSSZ},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				headers := http.Header{"Eth-Consensus-Version": {"gloas"}}
				if test.contentType != nil {
					headers["Content-Type"] = test.contentType
				}
				s := &Service{
					base: mustParseURL("http://example.com"), timeout: time.Second, log: zerolog.Nop(),
					client: &http.Client{Transport: responseLimitTransport(func(r *http.Request) (*http.Response, error) {
						require.Equal(t, method, r.Method)
						return &http.Response{StatusCode: http.StatusOK, Header: headers, ContentLength: -1, Body: io.NopCloser(strings.NewReader(test.body))}, nil
					})},
				}
				var response *httpResponse
				var err error
				if method == http.MethodGet {
					response, err = s.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = s.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, test.limits)
				}
				if test.wantError != "" {
					require.ErrorContains(t, err, test.wantError)
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(response.body))
					require.Equal(t, test.wantType, response.contentType)
				}
			})
		}
	}
}

func TestEPBSEndpointResponseLimits(t *testing.T) {
	include := false
	tests := []struct {
		name, path, method string
		call               func(*Service) error
		limit              int
	}{
		{"Proposal", "/eth/v4/validator/blocks/1", http.MethodPost, func(s *Service) error {
			_, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{}})
			return err
		}, maxEPBSProposalResponseSize},
		{"Envelope", "/eth/v1/validator/execution_payload_envelopes/", http.MethodGet, func(s *Service) error {
			_, err := s.ExecutionPayloadEnvelope(context.Background(), &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})
			return err
		}, maxEPBSResponseSize},
		{"SignedEnvelope", "/eth/v1/beacon/execution_payload_envelopes/head", http.MethodGet, func(s *Service) error {
			_, err := s.SignedExecutionPayloadEnvelope(context.Background(), &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}, maxEPBSResponseSize},
		{"AttestationData", "/eth/v1/validator/payload_attestation_data", http.MethodGet, func(s *Service) error {
			_, err := s.PayloadAttestationData(context.Background(), &api.PayloadAttestationDataOpts{Slot: 1})
			return err
		}, maxEPBSResponseSize},
		{"AttestationPool", "/eth/v1/beacon/pool/payload_attestations", http.MethodGet, func(s *Service) error {
			_, err := s.PayloadAttestationPool(context.Background(), &api.PayloadAttestationPoolOpts{})
			return err
		}, maxEPBSResponseSize},
	}
	for _, test := range tests {
		for _, encoding := range []ContentType{ContentTypeSSZ, ContentTypeJSON} {
			t.Run(test.name+"/"+encoding.String(), func(t *testing.T) {
				limit := test.limit
				if test.name == "Proposal" && encoding == ContentTypeJSON {
					limit = maxEPBSProposalJSONResponseSize
				}
				body := &responseLimitBody{}
				s := &Service{
					base: mustParseURL("http://example.com"), timeout: time.Second, log: zerolog.Nop(),
					connectionActive: true, connectionSynced: true,
					client: &http.Client{Transport: responseLimitTransport(func(r *http.Request) (*http.Response, error) {
						require.Equal(t, test.method, r.Method)
						require.True(t, strings.HasPrefix(r.URL.Path, test.path))
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {encoding.MediaType()}}, ContentLength: int64(limit) + 1, Body: body}, nil
					})},
				}
				err := test.call(s)
				require.ErrorContains(t, err, fmt.Sprintf("response body exceeds %d bytes", limit))
				require.False(t, body.read)
			})
		}
	}
}

func TestEPBSProposalAcceptsJSONLengthAboveSSZLimit(t *testing.T) {
	body := &responseLimitBody{}
	s := &Service{
		base: mustParseURL("http://example.com"), timeout: time.Second, log: zerolog.Nop(),
		connectionActive: true, connectionSynced: true,
		client: &http.Client{Transport: responseLimitTransport(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "application/octet-stream", r.Header.Get("Accept"))
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": {"application/json"}, "Eth-Consensus-Version": {"gloas"}},
				ContentLength: maxEPBSProposalResponseSize + 1,
				Body:          body,
			}, nil
		})},
	}
	include := false
	_, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{}})
	require.ErrorContains(t, err, "failed to decode gloas JSON epbs proposal")
	require.True(t, body.read)
}

func TestResponseLimitZeroIgnoresContentLength(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, encoding := range []ContentType{ContentTypeSSZ, ContentTypeJSON} {
			t.Run(method+"/"+encoding.String(), func(t *testing.T) {
				body := &responseLimitBody{}
				s := &Service{
					base: mustParseURL("http://example.com"), timeout: time.Second, log: zerolog.Nop(),
					client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
							"Content-Type": {encoding.MediaType()}, "Eth-Consensus-Version": {"gloas"},
						}, ContentLength: 6, Body: body}, nil
					})},
				}
				limits := responseLimits{ssz: 3, json: 3}
				if encoding == ContentTypeSSZ {
					limits.ssz = 0
				} else {
					limits.json = 0
				}
				var err error
				if method == http.MethodGet {
					_, err = s.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, limits)
				} else {
					_, err = s.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, limits)
				}
				require.NoError(t, err)
				require.True(t, body.read)
			})
		}
	}
}
