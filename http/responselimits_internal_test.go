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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestResponseLimitsByEncoding(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		limits      responseLimits
		declared    string
		err         string
	}{
		{name: "JSONFallback", contentType: "application/json; charset=utf-8", limits: responseLimits{ssz: 6, json: 7}},
		{name: "MissingContentType", limits: responseLimits{ssz: 6, json: 7}},
		{name: "UnknownContentType", contentType: "text/plain", limits: responseLimits{ssz: 6, json: 7}},
		{name: "SSZAtLimit", contentType: "application/octet-stream", limits: responseLimits{ssz: 7, json: 6}},
		{name: "SSZAboveLimit", contentType: "application/octet-stream", limits: responseLimits{ssz: 6, json: 7}, err: "response body exceeds 6 bytes"},
		{name: "JSONAboveLimit", contentType: "application/json", limits: responseLimits{ssz: 7, json: 6}, err: "response body exceeds 6 bytes"},
		{name: "UnboundedSSZ", contentType: "application/octet-stream", limits: responseLimits{json: 6}},
		{name: "UnboundedJSON", contentType: "application/json", limits: responseLimits{ssz: 6}},
		{name: "DeclaredSSZAboveLimit", contentType: "application/octet-stream", limits: responseLimits{ssz: 7, json: 20}, declared: "8", err: "response body exceeds 7 bytes"},
		{name: "DeclaredJSONAboveLimit", contentType: "application/json", limits: responseLimits{ssz: 20, json: 7}, declared: "8", err: "response body exceeds 7 bytes"},
	}

	for _, test := range tests {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(test.name+method, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if test.contentType == "" {
						w.Header()["Content-Type"] = nil
					} else {
						w.Header().Set("Content-Type", test.contentType)
					}
					if test.declared != "" {
						w.Header().Set("Content-Length", test.declared)
						w.WriteHeader(http.StatusOK)
						return
					}
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					_, _ = w.Write([]byte(`{"x":1}`))
				}))
				defer server.Close()

				base, err := url.Parse(server.URL)
				require.NoError(t, err)
				s := &Service{base: base, address: server.URL, client: server.Client(), timeout: time.Second, log: zerolog.Nop()}
				var response *httpResponse
				if method == http.MethodGet {
					response, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, test.limits)
				}
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
				} else {
					require.NoError(t, err)
					require.Equal(t, `{"x":1}`, string(response.body))
				}
			})
		}
	}
}
