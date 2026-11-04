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
	"net/url"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestReadResponseBody(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		limit int
		err   string
	}{
		{name: "WithinLimit", body: "1234", limit: 4},
		{name: "ExceedsLimit", body: "12345", limit: 4, err: "response body exceeds 4 bytes"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := readResponseBody(&http.Response{Body: io.NopCloser(strings.NewReader(test.body)), ContentLength: -1}, test.limit)
			if test.err != "" {
				require.EqualError(t, err, test.err)
				require.Nil(t, body)
			} else {
				require.NoError(t, err)
				require.Equal(t, []byte(test.body), body)
			}
		})
	}
}

// TestEPBSProposalResponseLimitsAreSurvivable pins the property that makes the
// block-production limits a defence rather than a comment.  readResponseBody
// reaches its length check only after io.ReadAll has buffered the whole body,
// and ReadAll grows its buffer geometrically, so peak allocation is close to
// twice the limit.  A limit taken from the protocol's theoretical maximum
// (~2GiB of SSZ, ~5GiB of JSON hex) is therefore never reached -- the process
// dies first -- and a body admitted just under one is equally fatal.
//
// The ceiling below is an allocation budget, not a protocol bound: every live
// preset produces a payload-included response orders of magnitude smaller, so
// the JSON limit sitting exactly at half the budget is the deliberate ceiling
// rather than a coincidence.  Raising either limit past it is the regression
// this test exists to catch.
func TestEPBSProposalResponseLimitsAreSurvivable(t *testing.T) {
	const maxProposalResponsePeakAllocation = 1024 * 1024 * 1024

	tests := []struct {
		name  string
		limit int
	}{
		{name: "SSZ", limit: maxEPBSProposalResponseSize},
		{name: "JSON", limit: maxEPBSProposalJSONResponseSize},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.LessOrEqual(t, 2*test.limit, maxProposalResponsePeakAllocation)
		})
	}
}

func TestResponseLimitsByEncoding(t *testing.T) {
	tests := []struct {
		name          string
		contentType   string
		body          string
		limits        responseLimits
		contentLength int64
		err           string
	}{
		{name: "JSONExceedsSSZLimit", contentType: "application/json; charset=utf-8", body: "null ", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1},
		{name: "JSONExactlyAtLimit", contentType: "application/json", body: "null    ", limits: responseLimits{ssz: 4, json: 8}, contentLength: 8},
		{name: "JSONOverLimit", contentType: "application/json", body: "null     ", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1, err: "response body exceeds 8 bytes"},
		{name: "SSZExactlyAtLimit", contentType: "application/octet-stream", body: "abcd", limits: responseLimits{ssz: 4, json: 8}, contentLength: 4},
		{name: "SSZOverLimit", contentType: "application/octet-stream", body: "abcde", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1, err: "response body exceeds 4 bytes"},
		{name: "MissingContentType", body: "null ", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1},
		{name: "UnknownContentType", contentType: "text/plain", body: "null ", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1},
		{name: "MissingContentTypeOverLimit", body: "null     ", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1, err: "response body exceeds 8 bytes"},
		{name: "UnknownContentTypeOverLimit", contentType: "text/plain", body: "null     ", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1, err: "response body exceeds 8 bytes"},
		{name: "JSONUnbounded", contentType: "application/json", body: "null     ", limits: responseLimits{ssz: 4}, contentLength: 9},
		{name: "SSZUnbounded", contentType: "application/octet-stream", body: "abcdefghi", limits: responseLimits{json: 8}, contentLength: 9},
		{name: "DeclaredJSONTooLong", contentType: "application/json", body: "null", limits: responseLimits{ssz: 4, json: 8}, contentLength: 9, err: "response body exceeds 8 bytes"},
		{name: "DeclaredSSZTooLong", contentType: "application/octet-stream", body: "abcd", limits: responseLimits{ssz: 4, json: 8}, contentLength: 5, err: "response body exceeds 4 bytes"},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				reads := 0
				base, err := url.Parse("http://example.com")
				require.NoError(t, err)
				s := &Service{
					base:    base,
					timeout: time.Second,
					log:     zerolog.Nop(),
					client: &http.Client{Transport: responseLimitRoundTripper(func(req *http.Request) (*http.Response, error) {
						require.Equal(t, method, req.Method)
						require.Contains(t, req.Header.Get("Accept"), "application/octet-stream")
						header := make(http.Header)
						if test.contentType != "" {
							header.Set("Content-Type", test.contentType)
						}
						return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.contentLength, Body: &countedResponseBody{Reader: strings.NewReader(test.body), reads: &reads}}, nil
					})},
				}
				var response *httpResponse
				if method == http.MethodGet {
					response, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, test.limits)
				}
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
					if test.contentLength > 0 {
						require.Zero(t, reads)
					}
					return
				}
				require.NoError(t, err)
				require.Equal(t, test.body, string(response.body))
				if test.contentType == "application/octet-stream" {
					require.Equal(t, ContentTypeSSZ, response.contentType)
				} else {
					require.Equal(t, ContentTypeJSON, response.contentType)
				}
			})
		}
	}
}

type responseLimitRoundTripper func(*http.Request) (*http.Response, error)

func (f responseLimitRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type countedResponseBody struct {
	io.Reader
	reads *int
}

func (b *countedResponseBody) Read(p []byte) (int, error) {
	*b.reads++
	return b.Reader.Read(p)
}

func (*countedResponseBody) Close() error { return nil }

func TestEPBSFetchResponseLimits(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Service) error
	}{
		{name: "ExecutionPayloadEnvelope", path: "/eth/v1/validator/execution_payload_envelopes/", call: func(s *Service) error {
			_, err := s.ExecutionPayloadEnvelope(context.Background(), &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})
			return err
		}},
		{name: "SignedExecutionPayloadEnvelope", path: "/eth/v1/beacon/execution_payload_envelopes/head", call: func(s *Service) error {
			_, err := s.SignedExecutionPayloadEnvelope(context.Background(), &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{name: "PayloadAttestationData", path: "/eth/v1/validator/payload_attestation_data", call: func(s *Service) error {
			_, err := s.PayloadAttestationData(context.Background(), &api.PayloadAttestationDataOpts{Slot: 1})
			return err
		}},
		{name: "PayloadAttestationPool", path: "/eth/v1/beacon/pool/payload_attestations", call: func(s *Service) error {
			_, err := s.PayloadAttestationPool(context.Background(), &api.PayloadAttestationPoolOpts{})
			return err
		}},
	}
	for _, test := range tests {
		for _, contentType := range []string{"application/json", "application/octet-stream"} {
			t.Run(test.name+"/"+contentType, func(t *testing.T) {
				base, err := url.Parse("http://example.com")
				require.NoError(t, err)
				reads := 0
				s := &Service{
					base:             base,
					timeout:          time.Second,
					log:              zerolog.Nop(),
					connectionActive: true,
					connectionSynced: true,
					client: &http.Client{Transport: responseLimitRoundTripper(func(req *http.Request) (*http.Response, error) {
						require.Equal(t, http.MethodGet, req.Method)
						require.True(t, strings.HasPrefix(req.URL.Path, test.path))
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, ContentLength: 64*1024*1024 + 1, Body: &countedResponseBody{Reader: strings.NewReader(""), reads: &reads}}, nil
					})},
				}
				err = test.call(s)
				require.ErrorContains(t, err, "response body exceeds 67108864 bytes")
				require.Zero(t, reads)
			})
		}
	}
}

func TestEPBSResponseLimitValues(t *testing.T) {
	require.Equal(t, 64*1024*1024, maxEPBSResponseSize)
	require.Equal(t, 256*1024*1024, maxEPBSProposalResponseSize)
	require.Equal(t, 512*1024*1024, maxEPBSProposalJSONResponseSize)
}

func TestReadResponseBodyReadError(t *testing.T) {
	want := errors.New("read failed")
	body, err := readResponseBody(&http.Response{Body: io.NopCloser(iotest.ErrReader(want)), ContentLength: -1}, 4)
	require.ErrorIs(t, err, want)
	require.Nil(t, body)
}

func TestUnboundedRequests(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			base, err := url.Parse("http://example.com")
			require.NoError(t, err)
			body := "null" + strings.Repeat(" ", 100)
			s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{
				Transport: responseLimitRoundTripper(func(req *http.Request) (*http.Response, error) {
					require.Equal(t, method, req.Method)
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body))}, nil
				}),
			}}
			var response *httpResponse
			if method == http.MethodGet {
				response, err = s.get(context.Background(), "/test", "", &api.CommonOpts{}, true)
			} else {
				response, err = s.post(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeJSON, nil)
			}
			require.NoError(t, err)
			require.Equal(t, body, string(response.body))
		})
	}
}
