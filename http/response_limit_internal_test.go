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
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type responseLimitTransport struct {
	response *http.Response
}

func (t responseLimitTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return t.response, nil
}

type countingResponseBody struct {
	io.Reader
	reads int
}

func (b *countingResponseBody) Read(p []byte) (int, error) {
	b.reads++
	return b.Reader.Read(p)
}

func (*countingResponseBody) Close() error { return nil }

// Small limits exercise the same GET and POST transport used by the public endpoints.
func TestResponseLimitsByEncoding(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		length      int64
		limits      responseLimits
		err         string
		noRead      bool
	}{
		{name: "SSZExactlyAtLimit", contentType: "application/octet-stream", body: "1234", length: -1, limits: responseLimits{ssz: 4, json: 6}},
		{name: "JSONExactlyAtLimit", contentType: "application/json; charset=utf-8", body: "123456", length: -1, limits: responseLimits{ssz: 4, json: 6}},
		{name: "JSONBeyondSSZLimit", contentType: "application/json", body: "12345", length: -1, limits: responseLimits{ssz: 4, json: 6}},
		{name: "MissingContentType", body: "12345", length: -1, limits: responseLimits{ssz: 4, json: 6}},
		{name: "UnknownContentType", contentType: "text/plain", body: "12345", length: -1, limits: responseLimits{ssz: 4, json: 6}},
		{name: "SSZExceeded", contentType: "application/octet-stream", body: "12345", length: -1, limits: responseLimits{ssz: 4, json: 6}, err: "response body exceeds 4 bytes"},
		{name: "JSONExceeded", contentType: "application/json", body: "1234567", length: -1, limits: responseLimits{ssz: 4, json: 6}, err: "response body exceeds 6 bytes"},
		{name: "MissingContentTypeExceeded", body: "1234567", length: -1, limits: responseLimits{ssz: 4, json: 6}, err: "response body exceeds 6 bytes"},
		{name: "SSZDeclaredTooLarge", contentType: "application/octet-stream", body: "1234", length: 5, limits: responseLimits{ssz: 4, json: 6}, err: "response body exceeds 4 bytes", noRead: true},
		{name: "JSONDeclaredTooLarge", contentType: "application/json", body: "1234", length: 7, limits: responseLimits{ssz: 4, json: 6}, err: "response body exceeds 6 bytes", noRead: true},
		{name: "SSZUnbounded", contentType: "application/octet-stream", body: "1234567", length: 7, limits: responseLimits{json: 6}},
		{name: "JSONUnbounded", contentType: "application/json", body: "1234567", length: 7, limits: responseLimits{ssz: 4}},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					body := &countingResponseBody{Reader: strings.NewReader(test.body)}
					header := make(http.Header)
					if test.contentType != "" {
						header.Set("Content-Type", test.contentType)
					}
					header.Set("Eth-Consensus-Version", "gloas")
					base, err := url.Parse("http://example.test")
					require.NoError(t, err)
					s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseLimitTransport{response: &http.Response{StatusCode: http.StatusOK, Header: header, Body: body, ContentLength: test.length}}}}
					var response *httpResponse
					if method == http.MethodGet {
						response, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
					} else {
						response, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, test.limits)
					}
					if test.err != "" {
						require.ErrorContains(t, err, test.err)
					} else {
						require.NoError(t, err)
						require.Equal(t, test.body, string(response.body))
					}
					if test.noRead {
						require.Zero(t, body.reads)
					} else {
						require.NotZero(t, body.reads)
					}
				})
			}
		})
	}
}
