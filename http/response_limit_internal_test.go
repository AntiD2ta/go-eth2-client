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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

type responseLimitTransport func(*http.Request) (*http.Response, error)

func (f responseLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type countedBody struct {
	io.Reader
	reads int
}

func (b *countedBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (*countedBody) Close() error                 { return nil }

func TestResponseLimitRejectsDeclaredLengthBeforeReading(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			body := &countedBody{Reader: strings.NewReader("12345")}
			base, address, err := parseAddress("http://example.test")
			require.NoError(t, err)
			service := &Service{
				base: base, address: address.String(), timeout: time.Second,
				client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, ContentLength: 5, Body: body}, nil
				})},
			}
			if method == http.MethodGet {
				_, err = service.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, responseLimits{ssz: 4, json: 4})
			} else {
				_, err = service.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, responseLimits{ssz: 4, json: 4})
			}
			require.ErrorContains(t, err, "response body exceeds 4 bytes")
			require.Zero(t, body.reads)
		})
	}
}

func TestEPBSProposalResponseLimitUsesResponseEncoding(t *testing.T) {
	for _, test := range []struct {
		name         string
		enforceJSON  bool
		responseType string
		wantLimit    bool
	}{
		{name: "JSONResponseToSSZRequest", responseType: "application/json"},
		{name: "SSZResponseToJSONRequest", enforceJSON: true, responseType: "application/octet-stream", wantLimit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &countedBody{Reader: strings.NewReader(`{}`)}
			base, address, err := parseAddress("http://example.test")
			require.NoError(t, err)
			service := &Service{
				base: base, address: address.String(), timeout: time.Second,
				connectionActive: true, connectionSynced: true, enforceJSON: test.enforceJSON,
				client: &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
					if test.enforceJSON {
						require.Equal(t, "application/json", req.Header.Get("Accept"))
					} else {
						require.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{test.responseType}}, ContentLength: 256*1024*1024 + 1, Body: body}, nil
				})},
			}
			include := false
			_, err = service.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 42, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}})
			require.Error(t, err)
			if test.wantLimit {
				require.ErrorContains(t, err, "response body exceeds 268435456 bytes")
				require.Zero(t, body.reads)
			} else {
				require.NotContains(t, err.Error(), "response body exceeds")
				require.Positive(t, body.reads)
			}
		})
	}
}

func TestResponseLimitsByEncoding(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range []struct {
			name         string
			contentTypes []string
			body         string
			limits       responseLimits
			wantError    string
		}{
			{name: "JSONAboveSSZLimit", contentTypes: []string{"application/json; charset=utf-8"}, body: "12345678", limits: responseLimits{ssz: 4, json: 8}},
			{name: "SSZAtLimit", contentTypes: []string{"application/octet-stream"}, body: "1234", limits: responseLimits{ssz: 4, json: 8}},
			{name: "SSZAboveLimit", contentTypes: []string{"application/octet-stream"}, body: "12345", limits: responseLimits{ssz: 4, json: 8}, wantError: "response body exceeds 4 bytes"},
			{name: "JSONAboveLimit", contentTypes: []string{"application/json"}, body: "123456789", limits: responseLimits{ssz: 4, json: 8}, wantError: "response body exceeds 8 bytes"},
			{name: "MissingTypeUsesJSON", body: "123456789", limits: responseLimits{ssz: 4, json: 8}, wantError: "response body exceeds 8 bytes"},
			{name: "UnknownTypeUsesJSON", contentTypes: []string{"text/plain"}, body: "123456789", limits: responseLimits{ssz: 4, json: 8}, wantError: "response body exceeds 8 bytes"},
			{name: "MultipleTypesUseJSON", contentTypes: []string{"application/octet-stream", "application/json"}, body: "12345", limits: responseLimits{ssz: 4, json: 8}},
			{name: "UnboundedSSZ", contentTypes: []string{"application/octet-stream"}, body: "123456789", limits: responseLimits{json: 4}},
			{name: "UnboundedJSON", contentTypes: []string{"application/json"}, body: "123456789", limits: responseLimits{ssz: 4}},
		} {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				base, address, err := parseAddress("http://example.test")
				require.NoError(t, err)
				headers := http.Header{"Eth-Consensus-Version": []string{"gloas"}}
				if test.contentTypes != nil {
					headers["Content-Type"] = test.contentTypes
				}
				service := &Service{base: base, address: address.String(), timeout: time.Second,
					client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: http.StatusOK, Header: headers, ContentLength: -1, Body: io.NopCloser(strings.NewReader(test.body))}, nil
					})},
				}
				var response *httpResponse
				if method == http.MethodGet {
					response, err = service.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = service.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, test.limits)
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

func TestEPBSFetchEndpointsHaveResponseLimits(t *testing.T) {
	for _, test := range []struct {
		name string
		call func(*Service) error
	}{
		{name: "ExecutionPayloadEnvelope", call: func(s *Service) error {
			_, err := s.ExecutionPayloadEnvelope(context.Background(), &api.ExecutionPayloadEnvelopeOpts{Slot: 42, BeaconBlockRoot: phase0.Root{1}})
			return err
		}},
		{name: "SignedExecutionPayloadEnvelope", call: func(s *Service) error {
			_, err := s.SignedExecutionPayloadEnvelope(context.Background(), &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{name: "PayloadAttestationData", call: func(s *Service) error {
			_, err := s.PayloadAttestationData(context.Background(), &api.PayloadAttestationDataOpts{Slot: 42})
			return err
		}},
		{name: "PayloadAttestationPool", call: func(s *Service) error {
			_, err := s.PayloadAttestationPool(context.Background(), &api.PayloadAttestationPoolOpts{})
			return err
		}},
	} {
		for _, contentType := range []string{"application/json", "application/octet-stream"} {
			t.Run(test.name+"/"+contentType, func(t *testing.T) {
				body := &countedBody{Reader: strings.NewReader("{}")}
				base, address, err := parseAddress("http://example.test")
				require.NoError(t, err)
				service := &Service{base: base, address: address.String(), timeout: time.Second, connectionActive: true, connectionSynced: true,
					client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, ContentLength: 64*1024*1024 + 1, Body: body}, nil
					})},
				}
				err = test.call(service)
				require.ErrorContains(t, err, "response body exceeds 67108864 bytes")
				require.Zero(t, body.reads)
			})
		}
	}
}
