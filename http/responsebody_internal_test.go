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
	reader := &countingResponseBody{}
	base, address, err := parseAddress("http://example.invalid")
	require.NoError(t, err)
	s := &Service{
		base: base, address: address.String(), timeout: time.Second,
		client: &http.Client{Transport: &responseRoundTripper{response: &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			ContentLength: 5, Body: reader,
		}}},
	}
	_, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, responseLimits{ssz: 4, json: 4})
	require.ErrorContains(t, err, "response body exceeds 4 bytes")
	require.Zero(t, reader.reads)
}

type countingResponseBody struct{ reads int }

func (b *countingResponseBody) Read(_ []byte) (int, error) {
	b.reads++
	return 0, io.EOF
}

func (*countingResponseBody) Close() error { return nil }

type responseRoundTripper struct {
	response *http.Response
	accept   string
	path     string
}

func (r *responseRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.accept = req.Header.Get("Accept")
	r.path = req.URL.Path
	return r.response, nil
}

func TestEPBSProposalUsesJSONResponseLimitForSSZRequest(t *testing.T) {
	reader := &countingResponseBody{}
	base, address, err := parseAddress("http://example.invalid")
	require.NoError(t, err)
	transport := &responseRoundTripper{response: &http.Response{
		StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}},
		ContentLength: maxEPBSProposalResponseSize + 1, Body: reader,
	}}
	s := &Service{base: base, address: address.String(), timeout: time.Second,
		client: &http.Client{Transport: transport}, connectionActive: true, connectionSynced: true}
	includePayload := false
	_, err = s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot: 1, IncludePayload: &includePayload, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}},
	})
	var apiErr *api.Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	require.Equal(t, "application/octet-stream", transport.accept)
	require.Positive(t, reader.reads)
}

func TestResponseLimitsByEncoding(t *testing.T) {
	tests := []struct {
		name, contentType, body string
		limits                  responseLimits
		declared                int64
		wantError               string
		wantRead                bool
	}{
		{name: "JSONWithinJSONLimit", contentType: "application/json", body: "12345", limits: responseLimits{ssz: 4, json: 6}, declared: -1, wantRead: true},
		{name: "JSONAtLimit", contentType: "application/json; charset=utf-8", body: "123456", limits: responseLimits{ssz: 4, json: 6}, declared: -1, wantRead: true},
		{name: "JSONOverLimit", contentType: "application/json", body: "1234567", limits: responseLimits{ssz: 4, json: 6}, declared: -1, wantError: "response body exceeds 6 bytes", wantRead: true},
		{name: "SSZAtLimit", contentType: "application/octet-stream", body: "1234", limits: responseLimits{ssz: 4, json: 6}, declared: -1, wantRead: true},
		{name: "SSZOverLimit", contentType: "application/octet-stream", body: "12345", limits: responseLimits{ssz: 4, json: 6}, declared: -1, wantError: "response body exceeds 4 bytes", wantRead: true},
		{name: "MissingContentTypeUsesJSON", body: "1234567", limits: responseLimits{ssz: 4, json: 6}, declared: -1, wantError: "response body exceeds 6 bytes", wantRead: true},
		{name: "UnknownContentTypeUsesJSON", contentType: "text/plain", body: "1234567", limits: responseLimits{ssz: 4, json: 6}, declared: -1, wantError: "response body exceeds 6 bytes", wantRead: true},
		{name: "DeclaredJSONOverLimit", contentType: "application/json", limits: responseLimits{ssz: 4, json: 6}, declared: 7, wantError: "response body exceeds 6 bytes"},
		{name: "DeclaredOneByteLimit", contentType: "application/json", limits: responseLimits{json: 1}, declared: 2, wantError: "response body exceeds 1 bytes"},
		{name: "DeclaredSSZOverLimit", contentType: "application/octet-stream", limits: responseLimits{ssz: 4, json: 6}, declared: 5, wantError: "response body exceeds 4 bytes"},
		{name: "DeclaredExactlyLimit", contentType: "application/json", body: "123456", limits: responseLimits{ssz: 4, json: 6}, declared: 6, wantRead: true},
		{name: "UnboundedJSON", contentType: "application/json", body: "1234567", limits: responseLimits{ssz: 4}, declared: 7, wantRead: true},
		{name: "UnboundedSSZ", contentType: "application/octet-stream", body: "1234567", limits: responseLimits{json: 4}, declared: 7, wantRead: true},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				body := &trackingBody{Reader: strings.NewReader(test.body)}
				header := http.Header{}
				if test.contentType != "" {
					header.Set("Content-Type", test.contentType)
				}
				transport := &responseRoundTripper{response: &http.Response{StatusCode: http.StatusBadRequest, Header: header, ContentLength: test.declared, Body: body}}
				base, address, err := parseAddress("http://example.invalid")
				require.NoError(t, err)
				s := &Service{base: base, address: address.String(), timeout: time.Second, client: &http.Client{Transport: transport}}
				if method == http.MethodGet {
					_, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					_, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, test.limits)
				}
				if test.wantError != "" {
					require.ErrorContains(t, err, test.wantError)
				} else {
					var apiErr *api.Error
					require.ErrorAs(t, err, &apiErr)
					require.Equal(t, []byte(test.body), apiErr.Data)
				}
				require.Equal(t, test.wantRead, body.reads > 0)
			})
		}
	}
}

type trackingBody struct {
	*strings.Reader
	reads int
}

func (b *trackingBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (*trackingBody) Close() error                 { return nil }

func TestEPBSEndpointResponseLimits(t *testing.T) {
	includePayload := false
	tests := []struct {
		name, path string
		limit      int64
		call       func(*Service) error
	}{
		{name: "Proposal", path: "/eth/v4/validator/blocks/1", limit: 256 * 1024 * 1024, call: func(s *Service) error {
			_, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, IncludePayload: &includePayload, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}})
			return err
		}},
		{name: "CachedEnvelope", path: "/eth/v1/validator/execution_payload_envelopes/1/", limit: 64 * 1024 * 1024, call: func(s *Service) error {
			_, err := s.ExecutionPayloadEnvelope(context.Background(), &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})
			return err
		}},
		{name: "SignedEnvelope", path: "/eth/v1/beacon/execution_payload_envelopes/head", limit: 64 * 1024 * 1024, call: func(s *Service) error {
			_, err := s.SignedExecutionPayloadEnvelope(context.Background(), &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{name: "AttestationData", path: "/eth/v1/validator/payload_attestation_data", limit: 64 * 1024 * 1024, call: func(s *Service) error {
			_, err := s.PayloadAttestationData(context.Background(), &api.PayloadAttestationDataOpts{Slot: 1})
			return err
		}},
		{name: "AttestationPool", path: "/eth/v1/beacon/pool/payload_attestations", limit: 64 * 1024 * 1024, call: func(s *Service) error {
			_, err := s.PayloadAttestationPool(context.Background(), &api.PayloadAttestationPoolOpts{})
			return err
		}},
	}
	for _, test := range tests {
		for _, contentType := range []string{"application/octet-stream", "application/json"} {
			t.Run(test.name+"/"+contentType, func(t *testing.T) {
				limit := test.limit
				if test.name == "Proposal" && contentType == "application/json" {
					limit = 512 * 1024 * 1024
				}
				body := &countingResponseBody{}
				transport := &responseRoundTripper{response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, ContentLength: limit + 1, Body: body}}
				base, address, err := parseAddress("http://example.invalid")
				require.NoError(t, err)
				s := &Service{base: base, address: address.String(), timeout: time.Second, client: &http.Client{Transport: transport}, connectionActive: true, connectionSynced: true}
				err = test.call(s)
				require.ErrorContains(t, err, fmt.Sprintf("response body exceeds %d bytes", limit))
				require.True(t, strings.HasPrefix(transport.path, test.path), transport.path)
				require.Zero(t, body.reads)
			})
		}
	}
}

func TestUnknownResponseContentTypeDecodesAsJSON(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, value := range []string{"text/plain", "application/octet-stream,application/json"} {
			t.Run(method+"/"+value, func(t *testing.T) {
				transport := &responseRoundTripper{response: &http.Response{StatusCode: http.StatusOK,
					Header: http.Header{"Content-Type": []string{value}}, ContentLength: -1,
					Body: io.NopCloser(strings.NewReader("{}"))}}
				base, address, err := parseAddress("http://example.invalid")
				require.NoError(t, err)
				s := &Service{base: base, address: address.String(), timeout: time.Second, client: &http.Client{Transport: transport}}
				var response *httpResponse
				if method == http.MethodGet {
					response, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, responseLimits{ssz: 1, json: 2})
				} else {
					response, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, responseLimits{ssz: 1, json: 2})
				}
				require.NoError(t, err)
				require.Equal(t, ContentTypeJSON, response.contentType)
				require.Equal(t, "{}", string(response.body))
			})
		}
	}
}
