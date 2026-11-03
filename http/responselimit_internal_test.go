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
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestResponseLimitRejectsDeclaredLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "5")
		_, _ = w.Write([]byte("1"))
	}))
	defer server.Close()
	base, address, err := parseAddress(server.URL)
	require.NoError(t, err)
	service := &Service{base: base, address: address.String(), client: server.Client(), timeout: time.Second}
	_, err = service.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, responseLimits{ssz: 4, json: 4})
	require.ErrorContains(t, err, "response body exceeds 4 bytes")
}

type responseLimitTransport struct {
	response *http.Response
	accept   string
}

func (transport *responseLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.accept = request.Header.Get("Accept")
	return transport.response, nil
}

type countingResponseBody struct {
	io.Reader
	reads int
}

func (body *countingResponseBody) Read(p []byte) (int, error) {
	body.reads++
	return body.Reader.Read(p)
}

func (*countingResponseBody) Close() error { return nil }

func TestResponseLimitsByReturnedEncoding(t *testing.T) {
	tests := []struct {
		name          string
		contentType   string
		body          string
		limits        responseLimits
		contentLength int64
		err           string
		unread        bool
	}{
		{name: "JSONFallback", contentType: "application/json; charset=utf-8", body: "1234", limits: responseLimits{ssz: 2, json: 4}, contentLength: -1},
		{name: "SSZAtLimit", contentType: "application/octet-stream", body: "12", limits: responseLimits{ssz: 2, json: 4}, contentLength: -1},
		{name: "SSZOverLimit", contentType: "application/octet-stream", body: "123", limits: responseLimits{ssz: 2, json: 4}, contentLength: -1, err: "response body exceeds 2 bytes"},
		{name: "DeclaredLengthAtLimit", contentType: "application/json", body: "1234", limits: responseLimits{ssz: 2, json: 4}, contentLength: 4},
		{name: "DeclaredLengthOverOne", contentType: "application/json", body: "1", limits: responseLimits{json: 1}, contentLength: 2, err: "response body exceeds 1 bytes", unread: true},
		{name: "JSONOverLimit", contentType: "application/json", body: "12345", limits: responseLimits{ssz: 2, json: 4}, contentLength: -1, err: "response body exceeds 4 bytes"},
		{name: "MissingContentType", body: "1234", limits: responseLimits{ssz: 2, json: 4}, contentLength: -1},
		{name: "MissingContentTypeOverLimit", body: "12345", limits: responseLimits{ssz: 2, json: 4}, contentLength: -1, err: "response body exceeds 4 bytes"},
		{name: "UnknownContentTypeOverLimit", contentType: "text/plain", body: "12345", limits: responseLimits{ssz: 2, json: 4}, contentLength: -1, err: "response body exceeds 4 bytes"},
		{name: "UnknownContentType", contentType: "text/plain", body: "1234", limits: responseLimits{ssz: 2, json: 4}, contentLength: -1},
		{name: "DeclaredLengthSSZ", contentType: "application/octet-stream", body: "1", limits: responseLimits{ssz: 2, json: 4}, contentLength: 3, err: "response body exceeds 2 bytes", unread: true},
		{name: "DeclaredLengthJSON", contentType: "application/json", body: "1", limits: responseLimits{ssz: 2, json: 4}, contentLength: 5, err: "response body exceeds 4 bytes", unread: true},
		{name: "UnboundedSSZ", contentType: "application/octet-stream", body: "12345", limits: responseLimits{json: 2}, contentLength: 5},
		{name: "UnboundedJSON", contentType: "application/json", body: "12345", limits: responseLimits{ssz: 2}, contentLength: 5},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				body := &countingResponseBody{Reader: strings.NewReader(test.body)}
				header := http.Header{"Eth-Consensus-Version": []string{"gloas"}}
				if test.contentType != "" {
					header.Set("Content-Type", test.contentType)
				}
				transport := &responseLimitTransport{response: &http.Response{StatusCode: http.StatusOK, Header: header, Body: body, ContentLength: test.contentLength}}
				base, address, err := parseAddress("http://example.com")
				require.NoError(t, err)
				service := &Service{base: base, address: address.String(), client: &http.Client{Transport: transport}, timeout: time.Second}
				var response *httpResponse
				if method == http.MethodGet {
					response, err = service.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = service.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, test.limits)
				}
				require.Contains(t, transport.accept, "application/octet-stream")
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
					require.Nil(t, response)
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(response.body))
					if test.contentType == "application/octet-stream" {
						require.Equal(t, ContentTypeSSZ, response.contentType)
					} else {
						require.Equal(t, ContentTypeJSON, response.contentType)
					}
				}
				if test.unread {
					require.Zero(t, body.reads)
				} else {
					require.Positive(t, body.reads)
				}
			})
		}
	}
}

func TestEPBSFetchResponseLimits(t *testing.T) {
	tests := []struct {
		name string
		call func(*Service) error
	}{
		{name: "ExecutionPayloadEnvelope", call: func(service *Service) error {
			_, err := service.ExecutionPayloadEnvelope(context.Background(), &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})
			return err
		}},
		{name: "SignedExecutionPayloadEnvelope", call: func(service *Service) error {
			_, err := service.SignedExecutionPayloadEnvelope(context.Background(), &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{name: "PayloadAttestationData", call: func(service *Service) error {
			_, err := service.PayloadAttestationData(context.Background(), &api.PayloadAttestationDataOpts{Slot: 1})
			return err
		}},
		{name: "PayloadAttestationPool", call: func(service *Service) error {
			_, err := service.PayloadAttestationPool(context.Background(), &api.PayloadAttestationPoolOpts{})
			return err
		}},
	}
	for _, test := range tests {
		for _, contentType := range []string{"application/octet-stream", "application/json"} {
			t.Run(test.name+"/"+contentType, func(t *testing.T) {
				body := &countingResponseBody{Reader: strings.NewReader("x")}
				transport := &responseLimitTransport{response: &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}},
					Body: body, ContentLength: 64*1024*1024 + 1,
				}}
				base, address, err := parseAddress("http://example.com")
				require.NoError(t, err)
				service := &Service{base: base, address: address.String(), client: &http.Client{Transport: transport},
					timeout: time.Second, connectionActive: true, connectionSynced: true}
				require.ErrorContains(t, test.call(service), "response body exceeds 67108864 bytes")
				require.Zero(t, body.reads)
			})
		}
	}
}
