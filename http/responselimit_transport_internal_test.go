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
	"io"
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/stretchr/testify/require"
)

type countingBody struct {
	io.Reader
	reads int
}

func (b *countingBody) Read(p []byte) (int, error) {
	b.reads++
	return b.Reader.Read(p)
}

func (*countingBody) Close() error { return nil }

func TestResponseLimitsForGETAndPOST(t *testing.T) {
	cases := []struct {
		name          string
		contentType   string
		body          string
		limits        responseLimits
		contentLength int64
		err           string
		unread        bool
	}{
		{name: "SSZExactlyAtLimit", contentType: "application/octet-stream", body: "null", limits: responseLimits{ssz: 4, json: 6}, contentLength: -1},
		{name: "SSZAboveLimit", contentType: "application/octet-stream", body: "null ", limits: responseLimits{ssz: 4, json: 6}, contentLength: -1, err: "response body exceeds 4 bytes"},
		{name: "JSONExactlyAtLimit", contentType: "application/json; charset=utf-8", body: "null  ", limits: responseLimits{ssz: 4, json: 6}, contentLength: -1},
		{name: "JSONAboveLimit", contentType: "application/json", body: "null   ", limits: responseLimits{ssz: 4, json: 6}, contentLength: -1, err: "response body exceeds 6 bytes"},
		{name: "MissingContentType", body: "null  ", limits: responseLimits{ssz: 4, json: 6}, contentLength: -1},
		{name: "MissingContentTypeAboveLimit", body: "null   ", limits: responseLimits{ssz: 4, json: 6}, contentLength: -1, err: "response body exceeds 6 bytes"},
		{name: "UnknownContentTypeWithinLimit", contentType: "text/plain", body: "null  ", limits: responseLimits{ssz: 4, json: 6}, contentLength: -1},
		{name: "UnknownContentType", contentType: "text/plain", body: "null   ", limits: responseLimits{ssz: 4, json: 6}, contentLength: -1, err: "response body exceeds 6 bytes"},
		{name: "DeclaredJSONAtLimit", contentType: "application/json", body: "null  ", limits: responseLimits{ssz: 4, json: 6}, contentLength: 6},
		{name: "DeclaredSSZTooLarge", contentType: "application/octet-stream", body: "null", limits: responseLimits{ssz: 4, json: 6}, contentLength: 5, err: "response body exceeds 4 bytes", unread: true},
		{name: "DeclaredJSONTooLarge", contentType: "application/json", body: "null", limits: responseLimits{ssz: 4, json: 6}, contentLength: 7, err: "response body exceeds 6 bytes", unread: true},
		{name: "UnboundedSSZ", contentType: "application/octet-stream", body: "null   ", limits: responseLimits{json: 4}, contentLength: 7},
		{name: "UnboundedJSON", contentType: "application/json", body: "null   ", limits: responseLimits{ssz: 4}, contentLength: 7},
	}

	for _, method := range []string{stdhttp.MethodGet, stdhttp.MethodPost} {
		for _, test := range cases {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				body := &countingBody{Reader: strings.NewReader(test.body)}
				service, ctx := responseLimitService(t, func(req *stdhttp.Request) *stdhttp.Response {
					header := make(stdhttp.Header)
					if test.contentType != "" {
						header.Set("Content-Type", test.contentType)
					}
					header.Set("Eth-Consensus-Version", "gloas")
					return &stdhttp.Response{StatusCode: stdhttp.StatusOK, Header: header, ContentLength: test.contentLength, Body: body, Request: req}
				})
				var response *httpResponse
				var err error
				if method == stdhttp.MethodGet {
					response, err = service.getWithResponseLimit(ctx, "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = service.postWithResponseLimit(ctx, "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, test.limits)
				}
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(response.body))
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
