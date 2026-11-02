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
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/stretchr/testify/require"
)

type responseLimitTransport func(*nethttp.Request) (*nethttp.Response, error)

func (f responseLimitTransport) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	return f(req)
}

type countedBody struct {
	io.Reader
	reads int
}

func (b *countedBody) Read(p []byte) (int, error) {
	b.reads++
	return b.Reader.Read(p)
}

func (*countedBody) Close() error { return nil }

func TestResponseLimitRejectsDeclaredLengthBeforeReading(t *testing.T) {
	for _, method := range []string{nethttp.MethodGet, nethttp.MethodPost} {
		t.Run(method, func(t *testing.T) {
			body := &countedBody{Reader: strings.NewReader("12345")}
			base, address, err := parseAddress("http://example.test")
			require.NoError(t, err)
			service := &Service{
				base: base, address: address.String(), timeout: time.Second,
				client: &nethttp.Client{Transport: responseLimitTransport(func(*nethttp.Request) (*nethttp.Response, error) {
					return &nethttp.Response{StatusCode: nethttp.StatusOK, Header: nethttp.Header{"Content-Type": {"application/json"}}, ContentLength: 5, Body: body}, nil
				})},
			}
			if method == nethttp.MethodGet {
				_, err = service.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, responseLimits{ssz: 4, json: 4})
			} else {
				_, err = service.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, responseLimits{ssz: 4, json: 4})
			}
			require.ErrorContains(t, err, "response body exceeds 4 bytes")
			require.Zero(t, body.reads)
		})
	}
}

func TestEPBSProposalUsesResponseEncodingForLimit(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		length      int64
		enforceJSON bool
		read        bool
		wantLimit   string
	}{
		{name: "JSON", contentType: "application/json", length: 256*1024*1024 + 1, read: true},
		{name: "SSZ", contentType: "application/octet-stream", length: 256*1024*1024 + 1, wantLimit: "268435456"},
		{name: "Missing", length: 256*1024*1024 + 1, read: true},
		{name: "JSONTooLarge", contentType: "application/json", length: 512*1024*1024 + 1, wantLimit: "536870912"},
		{name: "SSZToJSONRequest", contentType: "application/octet-stream", length: 256*1024*1024 + 1,
			enforceJSON: true, wantLimit: "268435456"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &countedBody{Reader: strings.NewReader("{}")}
			base, address, err := parseAddress("http://example.test")
			require.NoError(t, err)
			service := &Service{
				base: base, address: address.String(), timeout: time.Second,
				connectionActive: true, connectionSynced: true, enforceJSON: test.enforceJSON,
				client: &nethttp.Client{Transport: responseLimitTransport(func(req *nethttp.Request) (*nethttp.Response, error) {
					if test.enforceJSON {
						require.Equal(t, "application/json", req.Header.Get("Accept"))
					} else {
						require.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
					}
					header := nethttp.Header{}
					if test.contentType != "" {
						header.Set("Content-Type", test.contentType)
					}
					return &nethttp.Response{StatusCode: nethttp.StatusOK, Header: header, ContentLength: test.length, Body: body}, nil
				})},
			}
			includePayload := true
			_, err = service.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
				Slot: 123, IncludePayload: &includePayload, BuilderConfig: &gloas.BuilderConfig{},
			})
			require.Error(t, err)
			if test.read {
				require.NotContains(t, err.Error(), "response body exceeds")
				require.Positive(t, body.reads)
			} else {
				require.ErrorContains(t, err, "response body exceeds "+test.wantLimit+" bytes")
				require.Zero(t, body.reads)
			}
		})
	}
}

func TestResponseLimitsForGETAndPOST(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		body        string
		length      int64
		limits      responseLimits
		wantLimit   string
	}{
		{name: "JSONAtLimit", contentType: "application/json; charset=utf-8", body: "{}    ", length: 6, limits: responseLimits{ssz: 4, json: 6}},
		{name: "JSONOverLimit", contentType: "application/json", body: "{}     ", length: -1, limits: responseLimits{ssz: 4, json: 6}, wantLimit: "6"},
		{name: "SSZAtLimit", contentType: "application/octet-stream", body: "1234", length: 4, limits: responseLimits{ssz: 4, json: 6}},
		{name: "SSZOverLimit", contentType: "application/octet-stream", body: "12345", length: -1, limits: responseLimits{ssz: 4, json: 6}, wantLimit: "4"},
		{name: "MissingType", body: "{}    ", length: -1, limits: responseLimits{ssz: 4, json: 6}},
		{name: "UnknownType", contentType: "text/plain", body: "{}     ", length: -1, limits: responseLimits{ssz: 4, json: 6}, wantLimit: "6"},
		{name: "SSZUnbounded", contentType: "application/octet-stream", body: "1234567", length: 7, limits: responseLimits{json: 4}},
		{name: "JSONUnbounded", contentType: "application/json", body: "{}     ", length: 7, limits: responseLimits{ssz: 4}},
	} {
		for _, method := range []string{nethttp.MethodGet, nethttp.MethodPost} {
			t.Run(test.name+"/"+method, func(t *testing.T) {
				base, address, err := parseAddress("http://example.test")
				require.NoError(t, err)
				service := &Service{
					base: base, address: address.String(), timeout: time.Second,
					client: &nethttp.Client{Transport: responseLimitTransport(func(_ *nethttp.Request) (*nethttp.Response, error) {
						header := nethttp.Header{}
						if test.contentType != "" {
							header.Set("Content-Type", test.contentType)
						}
						header.Set("Eth-Consensus-Version", "gloas")
						return &nethttp.Response{StatusCode: nethttp.StatusOK, Header: header, ContentLength: test.length, Body: io.NopCloser(strings.NewReader(test.body))}, nil
					})},
				}
				var response *httpResponse
				if method == nethttp.MethodGet {
					response, err = service.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = service.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, test.limits)
				}
				if test.wantLimit != "" {
					require.ErrorContains(t, err, "response body exceeds "+test.wantLimit+" bytes")
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(response.body))
				}
			})
		}
	}
}
