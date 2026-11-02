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
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestResponseLimitJSONFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/octet-stream", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte("123456"))
		require.NoError(t, err)
	}))
	defer server.Close()

	base, address, err := parseAddress(server.URL)
	require.NoError(t, err)
	service := &Service{base: base, address: address.String(), client: server.Client(), timeout: time.Second}
	response, err := service.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, responseLimits{ssz: 4, json: 8})
	require.NoError(t, err)
	require.Equal(t, "123456", string(response.body))
}

type responseLimitTransport func(*http.Request) (*http.Response, error)

func (f responseLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type responseLimitBody struct {
	read bool
}

func (b *responseLimitBody) Read(_ []byte) (int, error) {
	b.read = true
	return 0, io.EOF
}

func (*responseLimitBody) Close() error { return nil }

func TestResponseLimitRejectsDeclaredLengthBeforeReading(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range []struct {
			name        string
			contentType string
			length      int64
			err         string
		}{
			{name: "JSON", contentType: "application/json", length: 9, err: "response body exceeds 8 bytes"},
			{name: "SSZ", contentType: "application/octet-stream", length: 5, err: "response body exceeds 4 bytes"},
			{name: "MissingType", length: 9, err: "response body exceeds 8 bytes"},
		} {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				body := &responseLimitBody{}
				header := http.Header{}
				if test.contentType != "" {
					header.Set("Content-Type", test.contentType)
				}
				client := &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.length, Body: body}, nil
				})}
				base, address, err := parseAddress("http://localhost")
				require.NoError(t, err)
				service := &Service{base: base, address: address.String(), client: client, timeout: time.Second}
				limits := responseLimits{ssz: 4, json: 8}
				if method == http.MethodGet {
					_, err = service.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, limits)
				} else {
					_, err = service.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, limits)
				}
				require.ErrorContains(t, err, test.err)
				require.False(t, body.read)
			})
		}
	}
}

func TestResponseLimitsByEncoding(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		length      int64
		limits      responseLimits
		err         string
	}{
		{name: "SSZAtLimit", contentType: "application/octet-stream", body: "1234", length: 4, limits: responseLimits{ssz: 4, json: 8}},
		{name: "SSZOverLimit", contentType: "application/octet-stream; charset=binary", body: "12345", limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 4 bytes"},
		{name: "JSONAtLimit", contentType: "application/json", body: "12345678", length: 8, limits: responseLimits{ssz: 4, json: 8}},
		{name: "JSONOverLimit", contentType: "application/json", body: "123456789", limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "MissingTypeUsesJSON", body: "123456", limits: responseLimits{ssz: 4, json: 8}},
		{name: "MissingTypeOverLimit", body: "123456789", limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "UnknownTypeUsesJSON", contentType: "text/plain", body: "123456", limits: responseLimits{ssz: 4, json: 8}},
		{name: "UnknownTypeOverLimit", contentType: "text/plain", body: "123456789", limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "UnboundedSSZ", contentType: "application/octet-stream", body: "123456789", length: 9, limits: responseLimits{json: 4}},
		{name: "UnboundedJSON", contentType: "application/json", body: "123456789", length: 9, limits: responseLimits{ssz: 4}},
	}

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				header := http.Header{"Eth-Consensus-Version": {"gloas"}}
				if test.contentType != "" {
					header.Set("Content-Type", test.contentType)
				}
				length := test.length
				if length == 0 {
					length = -1
				}
				client := &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: length, Body: io.NopCloser(strings.NewReader(test.body))}, nil
				})}
				base, address, err := parseAddress("http://localhost")
				require.NoError(t, err)
				service := &Service{base: base, address: address.String(), client: client, timeout: time.Second}
				var response *httpResponse
				if method == http.MethodGet {
					response, err = service.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = service.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, test.limits)
				}
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(response.body))
				}
			})
		}
	}
}

func TestEPBSProposalUsesReturnedEncodingLimit(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		length      int64
		err         string
	}{
		{name: "JSONBelowJSONLimit", contentType: "application/json", length: maxEPBSProposalResponseSize + 1, err: "no execution_payload_included"},
		{name: "JSONAboveJSONLimit", contentType: "application/json", length: maxEPBSProposalJSONResponseSize + 1, err: "response body exceeds 536870912 bytes"},
		{name: "SSZAboveSSZLimit", contentType: "application/octet-stream", length: maxEPBSProposalResponseSize + 1, err: "response body exceeds 268435456 bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var accept string
			client := &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
				accept = req.Header.Get("Accept")
				return &http.Response{
					StatusCode:    http.StatusOK,
					Header:        http.Header{"Content-Type": {test.contentType}, "Eth-Consensus-Version": {"gloas"}},
					ContentLength: test.length,
					Body:          io.NopCloser(strings.NewReader(`{}`)),
				}, nil
			})}
			base, address, err := parseAddress("http://localhost")
			require.NoError(t, err)
			service := &Service{base: base, address: address.String(), client: client, timeout: time.Second, connectionActive: true, connectionSynced: true}
			includePayload := false
			_, err = service.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
				Slot:           1,
				IncludePayload: &includePayload,
				BuilderConfig:  &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}},
			})
			require.ErrorContains(t, err, test.err)
			require.Equal(t, "application/octet-stream", accept)
		})
	}
}

func TestEPBSGetEndpointsEnforceLimits(t *testing.T) {
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
				body := &responseLimitBody{}
				var path string
				client := &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
					path = req.URL.Path
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, ContentLength: maxEPBSResponseSize + 1, Body: body}, nil
				})}
				base, address, err := parseAddress("http://localhost")
				require.NoError(t, err)
				service := &Service{base: base, address: address.String(), client: client, timeout: time.Second, connectionActive: true, connectionSynced: true}
				err = test.call(service)
				require.ErrorContains(t, err, "response body exceeds 67108864 bytes")
				require.True(t, strings.HasPrefix(path, test.path))
				require.False(t, body.read)
			})
		}
	}
}
