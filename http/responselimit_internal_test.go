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
	"github.com/stretchr/testify/require"
)

func TestEPBSProposalRejectsDeclaredOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "536870913")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	base, address, err := parseAddress(server.URL)
	require.NoError(t, err)
	service := &Service{
		base: base, address: address.String(), client: server.Client(), timeout: time.Second,
		connectionActive: true, connectionSynced: true, enforceJSON: true,
	}
	includePayload := false
	_, err = service.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot: 123, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}, IncludePayload: &includePayload,
	})
	require.ErrorContains(t, err, "response body exceeds 536870912 bytes")
}

func TestEPBSProposalUsesJSONLimitForSSZRequest(t *testing.T) {
	accept := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept <- r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "268435457")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	base, address, err := parseAddress(server.URL)
	require.NoError(t, err)
	service := &Service{
		base: base, address: address.String(), client: server.Client(), timeout: time.Second,
		connectionActive: true, connectionSynced: true,
	}
	includePayload := false
	_, err = service.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot: 123, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}, IncludePayload: &includePayload,
	})
	require.Equal(t, "application/octet-stream", <-accept)
	require.ErrorContains(t, err, "unexpected EOF")
}

type limitRoundTripper func(*http.Request) (*http.Response, error)

func (f limitRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type countedBody struct {
	io.Reader
	reads  int
	closed bool
}

func (b *countedBody) Read(p []byte) (int, error) {
	b.reads++
	return b.Reader.Read(p)
}

func (b *countedBody) Close() error {
	b.closed = true
	return nil
}

func TestResponseLimitsByEncoding(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		limits      responseLimits
		length      int64
		wantError   string
		unread      bool
	}{
		{name: "JSONExceedsSSZButWithinJSON", contentType: "application/json; charset=utf-8", body: "12345", limits: responseLimits{ssz: 4, json: 8}, length: -1},
		{name: "SSZAtLimit", contentType: "application/octet-stream", body: "1234", limits: responseLimits{ssz: 4, json: 8}, length: -1},
		{name: "SSZAboveLimit", contentType: "application/octet-stream", body: "12345", limits: responseLimits{ssz: 4, json: 8}, length: -1, wantError: "exceeds 4 bytes"},
		{name: "JSONAtLimit", contentType: "application/json", body: "12345678", limits: responseLimits{ssz: 4, json: 8}, length: -1},
		{name: "JSONAboveLimit", contentType: "application/json", body: "123456789", limits: responseLimits{ssz: 4, json: 8}, length: -1, wantError: "exceeds 8 bytes"},
		{name: "MissingContentType", body: "12345678", limits: responseLimits{ssz: 4, json: 8}, length: -1},
		{name: "MissingContentTypeAboveLimit", body: "123456789", limits: responseLimits{ssz: 4, json: 8}, length: -1, wantError: "exceeds 8 bytes"},
		{name: "UnknownContentType", contentType: "text/plain", body: "12345678", limits: responseLimits{ssz: 4, json: 8}, length: -1},
		{name: "UnknownContentTypeAboveLimit", contentType: "text/plain", body: "123456789", limits: responseLimits{ssz: 4, json: 8}, length: -1, wantError: "exceeds 8 bytes"},
		{name: "DeclaredSSZAboveLimit", contentType: "application/octet-stream", body: "x", limits: responseLimits{ssz: 4, json: 8}, length: 5, wantError: "exceeds 4 bytes", unread: true},
		{name: "DeclaredJSONAboveLimit", contentType: "application/json", body: "x", limits: responseLimits{ssz: 4, json: 8}, length: 9, wantError: "exceeds 8 bytes", unread: true},
		{name: "DeclaredJSONWithinLimit", contentType: "application/json", body: "12345", limits: responseLimits{ssz: 4, json: 8}, length: 5},
		{name: "DeclaredJSONAtLimit", contentType: "application/json", body: "12345678", limits: responseLimits{ssz: 4, json: 8}, length: 8},
		{name: "DeclaredSSZAtLimit", contentType: "application/octet-stream", body: "1234", limits: responseLimits{ssz: 4, json: 8}, length: 4},
		{name: "SSZUnbounded", contentType: "application/octet-stream", body: "123456789", limits: responseLimits{json: 4}, length: 9},
		{name: "JSONStillBounded", contentType: "application/json", body: "12345", limits: responseLimits{json: 4}, length: -1, wantError: "exceeds 4 bytes"},
		{name: "JSONUnbounded", contentType: "application/json", body: "123456789", limits: responseLimits{ssz: 4}, length: 9},
		{name: "SSZStillBounded", contentType: "application/octet-stream", body: "12345", limits: responseLimits{ssz: 4}, length: -1, wantError: "exceeds 4 bytes"},
	}

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				body := &countedBody{Reader: strings.NewReader(test.body)}
				headers := http.Header{"Eth-Consensus-Version": {"gloas"}}
				if test.contentType != "" {
					headers.Set("Content-Type", test.contentType)
				}
				transport := limitRoundTripper(func(r *http.Request) (*http.Response, error) {
					require.Equal(t, method, r.Method)
					return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: body, ContentLength: test.length}, nil
				})
				base, address, err := parseAddress("http://example.com")
				require.NoError(t, err)
				service := &Service{base: base, address: address.String(), client: &http.Client{Transport: transport}, timeout: time.Second}
				var response *httpResponse
				if method == http.MethodGet {
					response, err = service.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = service.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, test.limits)
				}
				if test.wantError != "" {
					require.ErrorContains(t, err, test.wantError)
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(response.body))
				}
				if test.unread {
					require.Zero(t, body.reads)
				} else {
					require.Positive(t, body.reads)
				}
				require.True(t, body.closed)
			})
		}
	}
}

func TestEPBSProposalResponseContentTypeLimits(t *testing.T) {
	tests := []struct {
		name, contentType, length, want string
	}{
		{name: "SSZ", contentType: "application/octet-stream", length: "268435457", want: "response body exceeds 268435456 bytes"},
		{name: "Missing", length: "536870913", want: "response body exceeds 536870912 bytes"},
		{name: "Unknown", contentType: "text/plain", length: "536870913", want: "response body exceeds 536870912 bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			accept := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				accept <- r.Header.Get("Accept")
				if test.contentType != "" {
					w.Header().Set("Content-Type", test.contentType)
				}
				w.Header().Set("Content-Length", test.length)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			base, address, err := parseAddress(server.URL)
			require.NoError(t, err)
			service := &Service{
				base: base, address: address.String(), client: server.Client(), timeout: time.Second,
				connectionActive: true, connectionSynced: true,
			}
			includePayload := false
			_, err = service.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
				Slot: 123, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}, IncludePayload: &includePayload,
			})
			require.Equal(t, "application/octet-stream", <-accept)
			require.ErrorContains(t, err, test.want)
		})
	}
}
