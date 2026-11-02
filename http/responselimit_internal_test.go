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
	"errors"
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

type responseRoundTripper func(*http.Request) (*http.Response, error)

func (f responseRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type unreadableBody struct{ read bool }

func (b *unreadableBody) Read([]byte) (int, error) {
	b.read = true
	return 0, errors.New("body was read")
}
func (*unreadableBody) Close() error { return nil }

func TestResponseLimitRejectsContentLengthBeforeReading(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, contentType := range []string{"application/json", "application/octet-stream"} {
			t.Run(method+"/"+contentType, func(t *testing.T) {
				body := &unreadableBody{}
				base, err := url.Parse("http://example.invalid")
				require.NoError(t, err)
				s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseRoundTripper(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, ContentLength: 5, Body: body}, nil
				})}}
				limits := responseLimits{ssz: 4, json: 4}
				if method == http.MethodGet {
					_, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, limits)
				} else {
					_, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeJSON, nil, limits)
				}
				require.ErrorContains(t, err, "response body exceeds 4 bytes")
				require.False(t, body.read)
			})
		}
	}
}

var _ io.ReadCloser = (*unreadableBody)(nil)

func TestEPBSProposalResponseLimits(t *testing.T) {
	tests := []struct {
		name         string
		enforceJSON  bool
		responseType string
		length       int64
		wantErr      string
		wantRead     bool
	}{
		{name: "JSONResponseToSSZRequest", responseType: "application/json", length: 256*1024*1024 + 1, wantErr: "body was read", wantRead: true},
		{name: "MissingContentType", length: 256*1024*1024 + 1, wantErr: "body was read", wantRead: true},
		{name: "JSONResponseOverLimit", responseType: "application/json", length: 512*1024*1024 + 1, wantErr: "response body exceeds 536870912 bytes"},
		{name: "SSZResponseToJSONRequest", enforceJSON: true, responseType: "application/octet-stream", length: 256*1024*1024 + 1, wantErr: "response body exceeds 268435456 bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &unreadableBody{}
			base, err := url.Parse("http://example.invalid")
			require.NoError(t, err)
			s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), connectionSynced: true, enforceJSON: test.enforceJSON, client: &http.Client{Transport: responseRoundTripper(func(r *http.Request) (*http.Response, error) {
				accept := "application/octet-stream"
				if test.enforceJSON {
					accept = "application/json"
				}
				require.Equal(t, accept, r.Header.Get("Accept"))
				header := http.Header{}
				if test.responseType != "" {
					header.Set("Content-Type", test.responseType)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.length, Body: body}, nil
			})}}
			include := false
			_, err = s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 123, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}, IncludePayload: &include})
			require.ErrorContains(t, err, test.wantErr)
			require.Equal(t, test.wantRead, body.read)
		})
	}
}

func TestResponseLimitsForGETAndPOST(t *testing.T) {
	tests := []struct {
		name           string
		contentType    string
		body           string
		limits         responseLimits
		declaredLength int64
		wantErr        string
	}{
		{name: "JSONAtLimit", contentType: "application/json; charset=utf-8", body: "1234567", limits: responseLimits{ssz: 4, json: 7}, declaredLength: -1},
		{name: "JSONOverLimit", contentType: "application/json", body: "12345678", limits: responseLimits{ssz: 4, json: 7}, declaredLength: -1, wantErr: "response body exceeds 7 bytes"},
		{name: "SSZAtLimit", contentType: "application/octet-stream", body: "1234", limits: responseLimits{ssz: 4, json: 7}, declaredLength: -1},
		{name: "SSZOverLimit", contentType: "application/octet-stream", body: "12345", limits: responseLimits{ssz: 4, json: 7}, declaredLength: -1, wantErr: "response body exceeds 4 bytes"},
		{name: "MissingContentType", body: "1234567", limits: responseLimits{ssz: 4, json: 7}, declaredLength: -1},
		{name: "MissingContentTypeOverLimit", body: "12345678", limits: responseLimits{ssz: 4, json: 7}, declaredLength: -1, wantErr: "response body exceeds 7 bytes"},
		{name: "UnknownContentType", contentType: "text/plain", body: "12345678", limits: responseLimits{ssz: 4, json: 7}, declaredLength: -1, wantErr: "response body exceeds 7 bytes"},
		{name: "SSZUnbounded", contentType: "application/octet-stream", body: "12345678", limits: responseLimits{json: 7}, declaredLength: 8},
		{name: "JSONUnbounded", contentType: "application/json", body: "12345678", limits: responseLimits{ssz: 4}, declaredLength: 8},
		{name: "JSONDeclaredTooLarge", contentType: "application/json", body: "1", limits: responseLimits{ssz: 4, json: 7}, declaredLength: 8, wantErr: "response body exceeds 7 bytes"},
		{name: "SSZDeclaredTooLarge", contentType: "application/octet-stream", body: "1", limits: responseLimits{ssz: 4, json: 7}, declaredLength: 5, wantErr: "response body exceeds 4 bytes"},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				base, err := url.Parse("http://example.invalid")
				require.NoError(t, err)
				s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseRoundTripper(func(r *http.Request) (*http.Response, error) {
					require.Equal(t, method, r.Method)
					header := http.Header{"Eth-Consensus-Version": {"gloas"}}
					if test.contentType != "" {
						header.Set("Content-Type", test.contentType)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.declaredLength, Body: io.NopCloser(strings.NewReader(test.body))}, nil
				})}}
				var response *httpResponse
				if method == http.MethodGet {
					response, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, test.limits)
				}
				if test.wantErr != "" {
					require.ErrorContains(t, err, test.wantErr)
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(response.body))
				}
			})
		}
	}
}

func TestEPBSReadEndpointsRejectOversizedResponses(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Service) error
	}{
		{name: "ExecutionPayloadEnvelope", path: "/eth/v1/validator/execution_payload_envelopes/", call: func(s *Service) error {
			_, err := s.ExecutionPayloadEnvelope(context.Background(), &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})
			return err
		}},
		{name: "SignedExecutionPayloadEnvelope", path: "/eth/v1/beacon/execution_payload_envelopes/", call: func(s *Service) error {
			_, err := s.SignedExecutionPayloadEnvelope(context.Background(), &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{name: "PayloadAttestationData", path: "/eth/v1/validator/payload_attestation_data", call: func(s *Service) error {
			_, err := s.PayloadAttestationData(context.Background(), &api.PayloadAttestationDataOpts{Slot: 1})
			return err
		}},
		{name: "PayloadAttestationPool", path: "/eth/v1/beacon/pool/payload_attestations", call: func(s *Service) error {
			_, err := s.PayloadAttestationPool(context.Background(), &api.PayloadAttestationPoolOpts{})
			return err
		}},
	}
	for _, test := range tests {
		for _, contentType := range []string{"application/json", "application/octet-stream"} {
			t.Run(test.name+"/"+contentType, func(t *testing.T) {
				body := &unreadableBody{}
				base, err := url.Parse("http://example.invalid")
				require.NoError(t, err)
				s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), connectionActive: true, connectionSynced: true, client: &http.Client{Transport: responseRoundTripper(func(r *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodGet, r.Method)
					require.True(t, strings.HasPrefix(r.URL.Path, test.path))
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, ContentLength: 64*1024*1024 + 1, Body: body}, nil
				})}}
				err = test.call(s)
				require.ErrorContains(t, err, "response body exceeds 67108864 bytes")
				require.False(t, body.read)
			})
		}
	}
}
