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
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/stretchr/testify/require"
)

type limitRoundTripper func(*http.Request) (*http.Response, error)

func (f limitRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type unreadableBody struct{ read bool }

func (b *unreadableBody) Read(_ []byte) (int, error) {
	b.read = true
	return 0, errors.New("body was read")
}
func (*unreadableBody) Close() error { return nil }

func TestEPBSProposalRejectsDeclaredOversizeBeforeReading(t *testing.T) {
	body := &unreadableBody{}
	s := &Service{
		base: mustParseURL("http://example.com"), timeout: time.Second, connectionSynced: true,
		client: &http.Client{Transport: limitRoundTripper(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "application/octet-stream", r.Header.Get("Accept"))
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: maxEPBSProposalJSONResponseSize + 1, Body: body}, nil
		})},
	}
	include := false
	_, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{}})
	require.ErrorContains(t, err, "response body exceeds 536870912 bytes")
	require.False(t, body.read)
}

var _ io.ReadCloser = (*unreadableBody)(nil)

func TestEPBSProposalResponseEncodingSelectsLimit(t *testing.T) {
	tests := []struct {
		name, contentType string
		wantError         string
		wantRead          bool
	}{
		{name: "JSON", contentType: "application/json", wantError: "body was read", wantRead: true},
		{name: "SSZ", contentType: "application/octet-stream", wantError: "response body exceeds 268435456 bytes"},
		{name: "MissingContentType", wantError: "body was read", wantRead: true},
		{name: "UnknownContentType", contentType: "application/unknown", wantError: "body was read", wantRead: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &unreadableBody{}
			headers := http.Header{}
			if test.contentType != "" {
				headers.Set("Content-Type", test.contentType)
			}
			s := &Service{base: mustParseURL("http://example.com"), timeout: time.Second, connectionSynced: true,
				client: &http.Client{Transport: limitRoundTripper(func(r *http.Request) (*http.Response, error) {
					require.Equal(t, "application/octet-stream", r.Header.Get("Accept"))
					return &http.Response{StatusCode: http.StatusOK, Header: headers, ContentLength: maxEPBSProposalResponseSize + 1, Body: body}, nil
				})},
			}
			include := false
			_, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{}})
			require.ErrorContains(t, err, test.wantError)
			require.Equal(t, test.wantRead, body.read)
		})
	}
}

func TestResponseLimitsForGETAndPOST(t *testing.T) {
	tests := []struct {
		name, contentType, body string
		length                  int64
		limits                  responseLimits
		wantError               string
	}{
		{name: "JSONAtLimit", contentType: "application/json", body: "{     }", length: -1, limits: responseLimits{ssz: 4, json: 7}},
		{name: "JSONOverLimit", contentType: "application/json", body: "{      }", length: -1, limits: responseLimits{ssz: 4, json: 7}, wantError: "response body exceeds 7 bytes"},
		{name: "SSZAtLimit", contentType: "application/octet-stream; charset=binary", body: "1234", length: -1, limits: responseLimits{ssz: 4, json: 7}},
		{name: "SSZOverLimit", contentType: "application/octet-stream", body: "12345", length: -1, limits: responseLimits{ssz: 4, json: 7}, wantError: "response body exceeds 4 bytes"},
		{name: "MissingContentType", body: "{     }", length: -1, limits: responseLimits{ssz: 4, json: 7}},
		{name: "UnknownContentType", contentType: "text/plain", body: "{      }", length: -1, limits: responseLimits{ssz: 4, json: 7}, wantError: "response body exceeds 7 bytes"},
		{name: "SSZUnbounded", contentType: "application/octet-stream", body: "12345678", length: 8, limits: responseLimits{ssz: 0, json: 4}},
		{name: "JSONUnbounded", contentType: "application/json", body: "{      }", length: 8, limits: responseLimits{ssz: 4, json: 0}},
		{name: "SSZDeclaredOversize", contentType: "application/octet-stream", body: "1234", length: 5, limits: responseLimits{ssz: 4, json: 7}, wantError: "response body exceeds 4 bytes"},
		{name: "JSONDeclaredOversize", contentType: "application/json", body: "{}", length: 8, limits: responseLimits{ssz: 4, json: 7}, wantError: "response body exceeds 7 bytes"},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				headers := http.Header{"Eth-Consensus-Version": {"gloas"}}
				if test.contentType != "" {
					headers.Set("Content-Type", test.contentType)
				}
				s := &Service{base: mustParseURL("http://example.com"), timeout: time.Second,
					client: &http.Client{Transport: limitRoundTripper(func(r *http.Request) (*http.Response, error) {
						require.Equal(t, method, r.Method)
						return &http.Response{StatusCode: http.StatusOK, Header: headers, ContentLength: test.length, Body: io.NopCloser(strings.NewReader(test.body))}, nil
					})},
				}
				var res *httpResponse
				var err error
				if method == http.MethodGet {
					res, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					res, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader("{}"), ContentTypeSSZ, nil, test.limits)
				}
				if test.wantError != "" {
					require.ErrorContains(t, err, test.wantError)
					return
				}
				require.NoError(t, err)
				require.Equal(t, test.body, string(res.body))
			})
		}
	}
}

func TestResponseLimitRejectsDeclaredLengthWithoutReading(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, contentType := range []string{"application/json", "application/octet-stream"} {
			t.Run(method+"/"+contentType, func(t *testing.T) {
				body := &unreadableBody{}
				s := &Service{base: mustParseURL("http://example.com"), timeout: time.Second,
					client: &http.Client{Transport: limitRoundTripper(func(_ *http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, ContentLength: 9, Body: body}, nil
					})},
				}
				limits := responseLimits{ssz: 4, json: 8}
				var err error
				if method == http.MethodGet {
					_, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, limits)
				} else {
					_, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader("{}"), ContentTypeSSZ, nil, limits)
				}
				require.ErrorContains(t, err, "response body exceeds")
				require.False(t, body.read)
			})
		}
	}
}
