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
	"sync/atomic"
	"testing"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestPostJSONResponseToSSZRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"a":1}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := New(ctx, WithAddress(server.URL), WithAllowDelayedStart(true), WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	response, err := service.(*Service).postWithResponseLimit(ctx, "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, responseLimits{ssz: 4, json: 8})
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, string(response.body))
}

type responseTestTransport func(*http.Request) (*http.Response, error)

func (f responseTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type responseTestBody struct {
	reader io.Reader
	reads  atomic.Int32
}

func (b *responseTestBody) Read(p []byte) (int, error) {
	b.reads.Add(1)
	return b.reader.Read(p)
}

func (*responseTestBody) Close() error { return nil }

func TestResponseEncodingLimits(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		length      int64
		limits      responseLimits
		err         string
		unread      bool
	}{
		{name: "JSONAtLimit", contentType: "application/json; charset=utf-8", body: `{"x":1} `, length: -1, limits: responseLimits{ssz: 4, json: 8}},
		{name: "JSONOverLimit", contentType: "application/json", body: `{"x":1}  `, length: -1, limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "SSZAtLimit", contentType: "application/octet-stream", body: "1234", length: -1, limits: responseLimits{ssz: 4, json: 8}},
		{name: "SSZOverLimit", contentType: "application/octet-stream", body: "12345", length: -1, limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 4 bytes"},
		{name: "MissingContentType", body: `{"x":1} `, length: -1, limits: responseLimits{ssz: 4, json: 8}},
		{name: "MissingContentTypeOverLimit", body: `{"x":1}  `, length: -1, limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "DeclaredAtLimit", contentType: "application/json", body: `{"x":1} `, length: 8, limits: responseLimits{ssz: 4, json: 8}},
		{name: "UnknownContentType", contentType: "text/plain", body: `{"x":1}  `, length: -1, limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "DeclaredJSONTooLarge", contentType: "application/json", body: "{}", length: 9, limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes", unread: true},
		{name: "DeclaredSSZTooLarge", contentType: "application/octet-stream", body: "1", length: 5, limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 4 bytes", unread: true},
		{name: "JSONUnbounded", contentType: "application/json", body: `{"x":1} `, length: 8, limits: responseLimits{ssz: 4}},
		{name: "SSZUnbounded", contentType: "application/octet-stream", body: "123456789", length: 9, limits: responseLimits{json: 4}},
	}

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				body := &responseTestBody{reader: strings.NewReader(test.body)}
				transport := responseTestTransport(func(req *http.Request) (*http.Response, error) {
					if req.URL.Path != "/test" {
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":{"version":"test","head_slot":"0","sync_distance":"0","is_syncing":false}}`)), Request: req}, nil
					}
					header := make(http.Header)
					if test.contentType != "" {
						header.Set("Content-Type", test.contentType)
					}
					header.Set("Eth-Consensus-Version", "gloas")
					return &http.Response{StatusCode: http.StatusOK, Header: header, Body: body, ContentLength: test.length, Request: req}, nil
				})
				service, err := New(ctx, WithAddress("http://example.test"), WithHTTPClient(&http.Client{Transport: transport}), WithAllowDelayedStart(true), WithLogLevel(zerolog.Disabled))
				require.NoError(t, err)
				s := service.(*Service)
				var response *httpResponse
				if method == http.MethodGet {
					response, err = s.getWithResponseLimit(ctx, "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = s.postWithResponseLimit(ctx, "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, test.limits)
				}
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
					require.Nil(t, response)
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(response.body))
				}
				if test.unread {
					require.Zero(t, body.reads.Load())
				}
			})
		}
	}
}
