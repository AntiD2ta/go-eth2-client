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
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"strings"
	"testing"

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
			body, err := readResponseBody(strings.NewReader(test.body), test.limit)
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

func TestPostRejectsDeclaredOversizeBeforeReading(t *testing.T) {
	body := &countingResponseBody{}
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	s := &Service{
		base:    base,
		timeout: time.Second,
		client: &http.Client{Transport: responseRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"application/json"}},
				ContentLength: 5,
				Body:          body,
			}, nil
		})},
	}
	_, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, responseLimits{ssz: 4, json: 4})
	require.ErrorContains(t, err, "response body exceeds 4 bytes")
	require.Zero(t, body.reads)
}

type responseRoundTripper func(*http.Request) (*http.Response, error)

func (f responseRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type countingResponseBody struct{ reads int }

func (b *countingResponseBody) Read([]byte) (int, error) {
	b.reads++
	return 0, io.EOF
}

func (*countingResponseBody) Close() error { return nil }

func TestPostUsesJSONLimitForJSONResponseToSSZRequest(t *testing.T) {
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	s := &Service{
		base:    base,
		timeout: time.Second,
		client: &http.Client{Transport: responseRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"application/json"}},
				ContentLength: 5,
				Body:          io.NopCloser(strings.NewReader("12345")),
			}, nil
		})},
	}
	res, err := s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, responseLimits{ssz: 4, json: 8})
	require.NoError(t, err)
	require.Equal(t, "12345", string(res.body))
}

func TestGetRejectsDeclaredOversizeBeforeReading(t *testing.T) {
	body := &countingResponseBody{}
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	s := &Service{
		base:    base,
		timeout: time.Second,
		client: &http.Client{Transport: responseRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"application/json"}},
				ContentLength: 5,
				Body:          body,
			}, nil
		})},
	}
	_, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, responseLimits{ssz: 4, json: 4})
	require.ErrorContains(t, err, "response body exceeds 4 bytes")
	require.Zero(t, body.reads)
}

func TestGetUsesJSONLimitForJSONResponseToSSZRequest(t *testing.T) {
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	s := &Service{
		base:    base,
		timeout: time.Second,
		client: &http.Client{Transport: responseRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"application/json"}, "Eth-Consensus-Version": []string{"gloas"}},
				ContentLength: 5,
				Body:          io.NopCloser(strings.NewReader("12345")),
			}, nil
		})},
	}
	res, err := s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, responseLimits{ssz: 4, json: 8})
	require.NoError(t, err)
	require.Equal(t, "12345", string(res.body))
}

func TestResponseLimitsByEncoding(t *testing.T) {
	tests := []struct {
		name          string
		contentTypes  []string
		body          string
		contentLength int64
		limits        responseLimits
		err           string
	}{
		{name: "JSONAtLimit", contentTypes: []string{"application/json; charset=utf-8"}, body: "12345678", contentLength: -1, limits: responseLimits{ssz: 4, json: 8}},
		{name: "JSONOverLimit", contentTypes: []string{"application/json"}, body: "123456789", contentLength: -1, limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "SSZAtLimit", contentTypes: []string{"application/octet-stream"}, body: "1234", contentLength: -1, limits: responseLimits{ssz: 4, json: 8}},
		{name: "SSZOverLimit", contentTypes: []string{"application/octet-stream"}, body: "12345", contentLength: -1, limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 4 bytes"},
		{name: "MissingContentType", body: "12345678", contentLength: -1, limits: responseLimits{ssz: 4, json: 8}},
		{name: "MissingContentTypeOverLimit", body: "123456789", contentLength: -1, limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "UnknownContentType", contentTypes: []string{"text/plain"}, body: "12345678", contentLength: -1, limits: responseLimits{ssz: 4, json: 8}},
		{name: "MultipleContentTypes", contentTypes: []string{"application/octet-stream", "application/json"}, body: "12345678", contentLength: -1, limits: responseLimits{ssz: 4, json: 8}},
		{name: "UnboundedJSON", contentTypes: []string{"application/json"}, body: "123456789", contentLength: 9, limits: responseLimits{ssz: 4}},
		{name: "UnboundedSSZ", contentTypes: []string{"application/octet-stream"}, body: "123456789", contentLength: 9, limits: responseLimits{json: 4}},
		{name: "JSONDeclaredAtLimit", contentTypes: []string{"application/json"}, body: "12345678", contentLength: 8, limits: responseLimits{ssz: 4, json: 8}},
		{name: "SSZDeclaredAtLimit", contentTypes: []string{"application/octet-stream"}, body: "1234", contentLength: 4, limits: responseLimits{ssz: 4, json: 8}},
	}

	requests := []struct {
		name   string
		method string
		json   bool
	}{
		{name: "GETSSZ", method: http.MethodGet},
		{name: "GETJSON", method: http.MethodGet, json: true},
		{name: "POSTSSZ", method: http.MethodPost},
		{name: "POSTJSON", method: http.MethodPost, json: true},
	}
	for _, request := range requests {
		for _, test := range tests {
			t.Run(request.name+"/"+test.name, func(t *testing.T) {
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				s := &Service{
					base:        base,
					timeout:     time.Second,
					enforceJSON: request.json,
					client: &http.Client{Transport: responseRoundTripper(func(req *http.Request) (*http.Response, error) {
						require.Equal(t, request.method, req.Method)
						header := http.Header{"Eth-Consensus-Version": []string{"gloas"}}
						if test.contentTypes != nil {
							header["Content-Type"] = test.contentTypes
						}
						return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.contentLength, Body: io.NopCloser(strings.NewReader(test.body))}, nil
					})},
				}
				var res *httpResponse
				if request.method == http.MethodGet {
					res, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					contentType := ContentTypeSSZ
					if request.json {
						contentType = ContentTypeJSON
					}
					res, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), contentType, nil, test.limits)
				}
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(res.body))
				}
			})
		}
	}
}

func TestEPBSProposalResponseLimit(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		length      int64
		err         string
		reads       int
	}{
		{name: "JSONUsesLargerLimit", contentType: "application/json", length: maxEPBSProposalResponseSize + 1, err: "no Eth-Consensus-Version header", reads: 1},
		{name: "MissingTypeUsesLargerLimit", length: maxEPBSProposalResponseSize + 1, err: "no Eth-Consensus-Version header", reads: 1},
		{name: "JSONOverLimit", contentType: "application/json", length: maxEPBSProposalJSONResponseSize + 1, err: "response body exceeds 536870912 bytes"},
		{name: "SSZOverLimit", contentType: "application/octet-stream", length: maxEPBSProposalResponseSize + 1, err: "response body exceeds 268435456 bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, err := url.Parse("http://example.test")
			require.NoError(t, err)
			body := &countingResponseBody{}
			s := &Service{
				base: base, timeout: time.Second, connectionSynced: true,
				client: &http.Client{Transport: responseRoundTripper(func(req *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodPost, req.Method)
					require.Equal(t, "/eth/v4/validator/blocks/1", req.URL.Path)
					require.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
					header := http.Header{}
					if test.contentType != "" {
						header.Set("Content-Type", test.contentType)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.length, Body: body}, nil
				})},
			}
			include := false
			_, err = s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{}})
			require.ErrorContains(t, err, test.err)
			require.Equal(t, test.reads, body.reads)
		})
	}
}

func TestEPBSGetResponseLimits(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Service) error
	}{
		{name: "ExecutionPayloadEnvelope", path: "/eth/v1/validator/execution_payload_envelopes/1/", call: func(s *Service) error {
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
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				body := &countingResponseBody{}
				s := &Service{base: base, timeout: time.Second, connectionActive: true, connectionSynced: true,
					client: &http.Client{Transport: responseRoundTripper(func(req *http.Request) (*http.Response, error) {
						require.Equal(t, http.MethodGet, req.Method)
						require.True(t, strings.HasPrefix(req.URL.Path, test.path))
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, ContentLength: maxEPBSResponseSize + 1, Body: body}, nil
					})},
				}
				err = test.call(s)
				require.ErrorContains(t, err, "response body exceeds 67108864 bytes")
				require.Zero(t, body.reads)
			})
		}
	}
}
