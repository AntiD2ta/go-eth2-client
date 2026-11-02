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

	"github.com/attestantio/go-eth2-client/api"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestResponseLimitsByEncoding(t *testing.T) {
	tests := []struct {
		name, contentType, body string
		limits                  responseLimits
		err                     string
	}{
		{name: "JSONExactlyLimit", contentType: "application/json; charset=utf-8", body: "null    ", limits: responseLimits{ssz: 4, json: 8}},
		{name: "JSONOverLimit", contentType: "application/json", body: "null     ", limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "SSZExactlyLimit", contentType: "application/octet-stream", body: "null", limits: responseLimits{ssz: 4, json: 8}},
		{name: "SSZOverLimit", contentType: "application/octet-stream", body: "null ", limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 4 bytes"},
		{name: "MissingTypeUsesJSON", body: "null    ", limits: responseLimits{ssz: 4, json: 8}},
		{name: "UnknownTypeUsesJSON", contentType: "text/plain", body: "null    ", limits: responseLimits{ssz: 4, json: 8}},
		{name: "SSZUnbounded", contentType: "application/octet-stream", body: "null    ", limits: responseLimits{json: 4}},
		{name: "JSONUnbounded", contentType: "application/json", body: "null    ", limits: responseLimits{ssz: 4}},
	}
	for _, test := range tests {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(test.name+method, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/eth/v1/node/version":
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"data":{"version":"test"}}`)
					case "/eth/v1/node/syncing":
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`)
					default:
						if test.contentType != "" {
							w.Header().Set("Content-Type", test.contentType)
						} else {
							w.Header()["Content-Type"] = nil
						}
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush() // Exercise a response without a declared length.
						_, _ = io.WriteString(w, test.body)
					}
				}))
				defer server.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				service, err := New(ctx, WithAddress(server.URL), WithAllowDelayedStart(true), WithLogLevel(zerolog.Disabled))
				require.NoError(t, err)
				s := service.(*Service)
				var res *httpResponse
				if method == http.MethodGet {
					res, err = s.getWithResponseLimit(ctx, "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					res, err = s.postWithResponseLimit(ctx, "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, test.limits)
				}
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
					return
				}
				require.NoError(t, err)
				require.Equal(t, test.body, string(res.body))
			})
		}
	}
}

type readCounter struct{ reads int }

func (r *readCounter) Read(_ []byte) (int, error) { r.reads++; return 0, io.EOF }
func (*readCounter) Close() error                 { return nil }

func TestResponseLimitRejectsLengthBeforeRead(t *testing.T) {
	body := &readCounter{}
	resp := &http.Response{Body: body, ContentLength: 9}
	_, err := readLimitedResponseBody(resp, ContentTypeJSON, responseLimits{ssz: 4, json: 8})
	require.ErrorContains(t, err, "response body exceeds 8 bytes")
	require.Zero(t, body.reads)
}
