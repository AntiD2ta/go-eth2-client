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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type responseLimitTransport struct {
	response *http.Response
	request  **http.Request
}

func (t responseLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.request != nil {
		*t.request = req
	}
	return t.response, nil
}

type observedBody struct {
	io.Reader
	read bool
}

func (b *observedBody) Read(p []byte) (int, error) {
	b.read = true
	return b.Reader.Read(p)
}

func (*observedBody) Close() error { return nil }

func TestEPBSProposalJSONResponseToSSZRequest(t *testing.T) {
	body := &observedBody{Reader: strings.NewReader(`{}`)}
	response := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": {"application/json"}},
		ContentLength: 300 * 1024 * 1024,
		Body:          body,
	}
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	var request *http.Request
	s := &Service{base: base, client: &http.Client{Transport: responseLimitTransport{response: response, request: &request}}, timeout: time.Second, log: zerolog.Nop(), connectionActive: true, connectionSynced: true}
	includePayload := false
	_, err = s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot: 1, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}, IncludePayload: &includePayload,
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "response body exceeds")
	require.True(t, body.read)
	require.Equal(t, "application/octet-stream", request.Header.Get("Accept"))
	require.Equal(t, "application/octet-stream", request.Header.Get("Content-Type"))
}

func TestResponseLimitRejectsDeclaredLengthBeforeReading(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, limit := range []int{1, 4} {
			t.Run(fmt.Sprintf("%s/%d", method, limit), func(t *testing.T) {
				body := &observedBody{Reader: strings.NewReader(`{}`)}
				response := &http.Response{
					StatusCode:    http.StatusOK,
					Header:        http.Header{"Content-Type": {"application/json"}},
					ContentLength: int64(limit + 1),
					Body:          body,
				}
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				s := &Service{base: base, client: &http.Client{Transport: responseLimitTransport{response: response}}, timeout: time.Second, log: zerolog.Nop()}
				limits := responseLimits{ssz: limit, json: limit}
				if method == http.MethodGet {
					_, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, limits)
				} else {
					_, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, limits)
				}
				require.ErrorContains(t, err, fmt.Sprintf("response body exceeds %d bytes", limit))
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
		limits      responseLimits
		wantType    ContentType
		err         string
	}{
		{name: "JSONFallback", contentType: "application/json; charset=utf-8", body: `{}` + "     ", limits: responseLimits{ssz: 4, json: 8}, wantType: ContentTypeJSON},
		{name: "JSONExact", contentType: "application/json", body: `{}` + "      ", limits: responseLimits{ssz: 4, json: 8}, wantType: ContentTypeJSON},
		{name: "JSONTooLarge", contentType: "application/json", body: `{}` + "       ", limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "SSZExact", contentType: "application/octet-stream", body: "1234", limits: responseLimits{ssz: 4, json: 8}, wantType: ContentTypeSSZ},
		{name: "SSZTooLarge", contentType: "application/octet-stream", body: "12345", limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 4 bytes"},
		{name: "MissingType", body: `{}` + "     ", limits: responseLimits{ssz: 4, json: 8}, wantType: ContentTypeJSON},
		{name: "MissingTypeTooLarge", body: `{}` + "       ", limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "UnknownType", contentType: "text/plain", body: `{}` + "     ", limits: responseLimits{ssz: 4, json: 8}, wantType: ContentTypeJSON},
		{name: "JSONUnbounded", contentType: "application/json", body: `{}` + "        ", limits: responseLimits{ssz: 4}, wantType: ContentTypeJSON},
		{name: "SSZUnbounded", contentType: "application/octet-stream", body: "123456789", limits: responseLimits{json: 4}, wantType: ContentTypeSSZ},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				header := http.Header{}
				if test.contentType != "" {
					header.Set("Content-Type", test.contentType)
				}
				length := int64(-1)
				if strings.HasSuffix(test.name, "Unbounded") || strings.HasSuffix(test.name, "Exact") {
					length = int64(len(test.body))
				}
				response := &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: length, Body: io.NopCloser(strings.NewReader(test.body))}
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				s := &Service{base: base, client: &http.Client{Transport: responseLimitTransport{response: response}}, timeout: time.Second, log: zerolog.Nop()}
				var res *httpResponse
				if method == http.MethodGet {
					res, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					res, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, test.limits)
				}
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
					return
				}
				require.NoError(t, err)
				require.Equal(t, test.wantType, res.contentType)
				require.Equal(t, test.body, string(res.body))
			})
		}
	}
}

func TestEPBSEndpointResponseLimits(t *testing.T) {
	tests := []struct {
		name string
		call func(*Service) error
	}{
		{name: "ExecutionPayloadEnvelope", call: func(s *Service) error {
			_, err := s.ExecutionPayloadEnvelope(context.Background(), &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})
			return err
		}},
		{name: "SignedExecutionPayloadEnvelope", call: func(s *Service) error {
			_, err := s.SignedExecutionPayloadEnvelope(context.Background(), &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{name: "PayloadAttestationData", call: func(s *Service) error {
			_, err := s.PayloadAttestationData(context.Background(), &api.PayloadAttestationDataOpts{Slot: 1})
			return err
		}},
		{name: "PayloadAttestationPool", call: func(s *Service) error {
			_, err := s.PayloadAttestationPool(context.Background(), &api.PayloadAttestationPoolOpts{})
			return err
		}},
	}
	for _, test := range tests {
		for _, contentType := range []string{"application/json", "application/octet-stream"} {
			t.Run(test.name+"/"+contentType, func(t *testing.T) {
				body := &observedBody{Reader: strings.NewReader(`{}`)}
				response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, ContentLength: 64*1024*1024 + 1, Body: body}
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				s := &Service{base: base, client: &http.Client{Transport: responseLimitTransport{response: response}}, timeout: time.Second, log: zerolog.Nop(), connectionActive: true, connectionSynced: true}
				err = test.call(s)
				require.ErrorContains(t, err, "response body exceeds 67108864 bytes")
				require.False(t, body.read)
			})
		}
	}
}

func TestEPBSProposalResponseSizeLimits(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		length      int64
		limit       string
	}{
		{name: "SSZ", contentType: "application/octet-stream", length: 256*1024*1024 + 1, limit: "268435456"},
		{name: "JSON", contentType: "application/json", length: 512*1024*1024 + 1, limit: "536870912"},
		{name: "MissingType", length: 512*1024*1024 + 1, limit: "536870912"},
		{name: "UnknownType", contentType: "text/plain", length: 512*1024*1024 + 1, limit: "536870912"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &observedBody{Reader: strings.NewReader(`{}`)}
			header := http.Header{}
			if test.contentType != "" {
				header.Set("Content-Type", test.contentType)
			}
			response := &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.length, Body: body}
			base, err := url.Parse("http://example.test")
			require.NoError(t, err)
			s := &Service{base: base, client: &http.Client{Transport: responseLimitTransport{response: response}}, timeout: time.Second, log: zerolog.Nop(), connectionActive: true, connectionSynced: true}
			includePayload := false
			_, err = s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
				Slot: 1, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}, IncludePayload: &includePayload,
			})
			require.ErrorContains(t, err, "response body exceeds "+test.limit+" bytes")
			require.False(t, body.read)
		})
	}
}

func TestUnboundedRequests(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			const body = `{"data":1}`
			response := &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": {"application/json"}},
				ContentLength: int64(len(body)),
				Body:          io.NopCloser(strings.NewReader(body)),
			}
			base, err := url.Parse("http://example.test")
			require.NoError(t, err)
			s := &Service{base: base, client: &http.Client{Transport: responseLimitTransport{response: response}}, timeout: time.Second, log: zerolog.Nop()}
			var res *httpResponse
			if method == http.MethodGet {
				res, err = s.get(context.Background(), "/test", "", &api.CommonOpts{}, true)
			} else {
				res, err = s.post(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeJSON, nil)
			}
			require.NoError(t, err)
			require.Equal(t, body, string(res.body))
		})
	}
}
