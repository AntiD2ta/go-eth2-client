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
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type responseLimitTransport func(*http.Request) (*http.Response, error)

func (f responseLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type responseLimitBody struct{ reads int }

func (b *responseLimitBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }
func (*responseLimitBody) Close() error               { return nil }

func TestResponseLimitRejectsDeclaredLengthBeforeReading(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			body := &responseLimitBody{}
			base, err := url.Parse("http://example.test")
			require.NoError(t, err)
			s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: 5, Body: body}, nil
			})}}
			if method == http.MethodGet {
				_, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, responseLimits{ssz: 4, json: 4})
			} else {
				_, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, responseLimits{ssz: 4, json: 4})
			}
			require.ErrorContains(t, err, "response body exceeds 4 bytes")
			require.Zero(t, body.reads)
		})
	}
}

func TestResponseLimitUsesResponseEncoding(t *testing.T) {
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: -1, Body: io.NopCloser(strings.NewReader(`{"a":1}`))}, nil
	})}}
	response, err := s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, responseLimits{ssz: 4, json: 7})
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, string(response.body))
}

func TestResponseLimitsByEncodingAndMethod(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		limits      responseLimits
		wantError   string
	}{
		{name: "SSZExact", contentType: "application/octet-stream", body: "1234", limits: responseLimits{ssz: 4, json: 7}},
		{name: "SSZOver", contentType: "application/octet-stream", body: "12345", limits: responseLimits{ssz: 4, json: 7}, wantError: "exceeds 4 bytes"},
		{name: "JSONExact", contentType: "application/json; charset=utf-8", body: `{"a":1}`, limits: responseLimits{ssz: 4, json: 7}},
		{name: "JSONOver", contentType: "application/json", body: `{"ab":1}`, limits: responseLimits{ssz: 4, json: 7}, wantError: "exceeds 7 bytes"},
		{name: "MissingType", body: `{"a":1}`, limits: responseLimits{ssz: 4, json: 7}},
		{name: "MissingTypeOver", body: `{"ab":1}`, limits: responseLimits{ssz: 4, json: 7}, wantError: "exceeds 7 bytes"},
		{name: "UnknownType", contentType: "text/plain", body: `{"a":1}`, limits: responseLimits{ssz: 4, json: 7}},
		{name: "UnknownTypeOver", contentType: "text/plain", body: `{"ab":1}`, limits: responseLimits{ssz: 4, json: 7}, wantError: "exceeds 7 bytes"},
		{name: "SSZUnbounded", contentType: "application/octet-stream", body: "12345678", limits: responseLimits{json: 4}},
		{name: "JSONUnbounded", contentType: "application/json", body: `{"ab":1}`, limits: responseLimits{ssz: 4}},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
					require.Equal(t, method, req.Method)
					require.Contains(t, req.Header.Get("Accept"), "application/octet-stream")
					header := http.Header{"Eth-Consensus-Version": {"gloas"}}
					if test.contentType != "" {
						header.Set("Content-Type", test.contentType)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: -1, Body: io.NopCloser(strings.NewReader(test.body))}, nil
				})}}
				var response *httpResponse
				if method == http.MethodGet {
					response, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, test.limits)
				}
				if test.wantError != "" {
					require.ErrorContains(t, err, test.wantError)
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(response.body))
				}
			})
		}
	}
}

func TestEPBSProposalResponseLimitsFollowResponse(t *testing.T) {
	tests := []struct {
		name, contentType string
		length            int64
		wantLimit         int
	}{
		{name: "SSZOver", contentType: "application/octet-stream", length: 256*1024*1024 + 1, wantLimit: 256 * 1024 * 1024},
		{name: "JSONAboveSSZLimit", contentType: "application/json", length: 256*1024*1024 + 1},
		{name: "MissingTypeAboveSSZLimit", length: 256*1024*1024 + 1},
		{name: "JSONOver", contentType: "application/json", length: 512*1024*1024 + 1, wantLimit: 512 * 1024 * 1024},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &responseLimitBody{}
			base, err := url.Parse("http://example.test")
			require.NoError(t, err)
			s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), connectionSynced: true, client: &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, req.Method)
				require.Equal(t, "/eth/v4/validator/blocks/123", req.URL.Path)
				require.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
				header := http.Header{"Eth-Consensus-Version": {"gloas"}}
				if test.contentType != "" {
					header.Set("Content-Type", test.contentType)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.length, Body: body}, nil
			})}}
			include := false
			_, err = s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 123, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}})
			require.Error(t, err)
			if test.wantLimit != 0 {
				require.ErrorContains(t, err, fmt.Sprintf("response body exceeds %d bytes", test.wantLimit))
				require.Zero(t, body.reads)
			} else {
				require.NotContains(t, err.Error(), "response body exceeds")
				require.Positive(t, body.reads)
			}
		})
	}
}

func TestEPBSGetEndpointsRejectOversizedResponses(t *testing.T) {
	tests := []struct {
		name, path string
		call       func(*Service) error
	}{
		{name: "CachedEnvelope", path: "/eth/v1/validator/execution_payload_envelopes/", call: func(s *Service) error {
			_, err := s.ExecutionPayloadEnvelope(context.Background(), &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})
			return err
		}},
		{name: "SignedEnvelope", path: "/eth/v1/beacon/execution_payload_envelopes/", call: func(s *Service) error {
			_, err := s.SignedExecutionPayloadEnvelope(context.Background(), &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{name: "AttestationData", path: "/eth/v1/validator/payload_attestation_data", call: func(s *Service) error {
			_, err := s.PayloadAttestationData(context.Background(), &api.PayloadAttestationDataOpts{Slot: 1})
			return err
		}},
		{name: "AttestationPool", path: "/eth/v1/beacon/pool/payload_attestations", call: func(s *Service) error {
			_, err := s.PayloadAttestationPool(context.Background(), &api.PayloadAttestationPoolOpts{})
			return err
		}},
	}
	for _, test := range tests {
		for _, contentType := range []string{"application/json", "application/octet-stream"} {
			t.Run(test.name+"/"+contentType, func(t *testing.T) {
				body := &responseLimitBody{}
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), connectionActive: true, connectionSynced: true, client: &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodGet, req.Method)
					require.True(t, strings.HasPrefix(req.URL.Path, test.path))
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, ContentLength: 64*1024*1024 + 1, Body: body}, nil
				})}}
				err = test.call(s)
				require.ErrorContains(t, err, "response body exceeds 67108864 bytes")
				require.Zero(t, body.reads)
			})
		}
	}
}
