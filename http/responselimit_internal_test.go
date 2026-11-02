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
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type responseLimitTransport func(*http.Request) (*http.Response, error)

func (f responseLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type responseLimitBody struct{ read bool }

func (b *responseLimitBody) Read(_ []byte) (int, error) { b.read = true; return 0, io.EOF }
func (*responseLimitBody) Close() error                 { return nil }

func TestPostRejectsDeclaredOversizedBodyBeforeReading(t *testing.T) {
	body := &responseLimitBody{}
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: 5, Body: body}, nil
	})}}
	_, err = s.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader("{}"), ContentTypeJSON, nil, responseLimits{json: 4})
	require.ErrorContains(t, err, "response body exceeds 4 bytes")
	require.False(t, body.read)
}

// A response inside the JSON budget must reach the body reader even when the request was SSZ.
func TestEPBSProposalUsesJSONResponseLimitForSSZRequest(t *testing.T) {
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	marker := errors.New("body read reached")
	s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), connectionSynced: true, client: &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: maxEPBSProposalResponseSize + 1, Body: io.NopCloser(errorReader{marker})}, nil
	})}}
	include := false
	_, err = s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{}})
	require.ErrorIs(t, err, marker)
}

type errorReader struct{ err error }

func (r errorReader) Read(_ []byte) (int, error) { return 0, r.err }

func TestGetRejectsDeclaredOversizedBodyBeforeReading(t *testing.T) {
	body := &responseLimitBody{}
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: 5, Body: body}, nil
	})}}
	_, err = s.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, responseLimits{ssz: 4, json: 4})
	require.ErrorContains(t, err, "response body exceeds 4 bytes")
	require.False(t, body.read)
}

func TestGetUsesJSONResponseLimitForSSZRequest(t *testing.T) {
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
		require.Contains(t, req.Header.Get("Accept"), "application/octet-stream")
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}, "Eth-Consensus-Version": {"gloas"}}, ContentLength: -1, Body: io.NopCloser(strings.NewReader("12345"))}, nil
	})}}
	response, err := s.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, responseLimits{ssz: 4, json: 8})
	require.NoError(t, err)
	require.Equal(t, "12345", string(response.body))
}

func TestResponseLimitsFollowResponseEncoding(t *testing.T) {
	tests := []struct {
		name          string
		contentType   string
		body          string
		limits        responseLimits
		contentLength int64
		wantError     string
	}{
		{name: "JSONAtLimit", contentType: "application/json; charset=utf-8", body: "12345678", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1},
		{name: "JSONOverLimit", contentType: "application/json", body: "123456789", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1, wantError: "response body exceeds 8 bytes"},
		{name: "SSZAtLimit", contentType: "application/octet-stream", body: "1234", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1},
		{name: "SSZOverLimit", contentType: "application/octet-stream", body: "12345", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1, wantError: "response body exceeds 4 bytes"},
		{name: "MissingContentType", body: "12345678", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1},
		{name: "UnknownContentType", contentType: "text/plain", body: "123456789", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1, wantError: "response body exceeds 8 bytes"},
		{name: "MissingContentTypeOver", body: "123456789", limits: responseLimits{ssz: 4, json: 8}, contentLength: -1, wantError: "response body exceeds 8 bytes"},
		{name: "SSZUnbounded", contentType: "application/octet-stream", body: "123456789", limits: responseLimits{json: 4}, contentLength: 9},
		{name: "JSONUnbounded", contentType: "application/json", body: "123456789", limits: responseLimits{ssz: 4}, contentLength: 9},
		{name: "DeclaredSSZOverLimit", contentType: "application/octet-stream", limits: responseLimits{ssz: 4, json: 8}, contentLength: 5, wantError: "response body exceeds 4 bytes"},
		{name: "DeclaredJSONOverLimit", contentType: "application/json", limits: responseLimits{ssz: 4, json: 8}, contentLength: 9, wantError: "response body exceeds 8 bytes"},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
					require.Equal(t, method, req.Method)
					header := http.Header{"Eth-Consensus-Version": {"gloas"}}
					if test.contentType != "" {
						header.Set("Content-Type", test.contentType)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.contentLength, Body: io.NopCloser(strings.NewReader(test.body))}, nil
				})}}
				var response *httpResponse
				if method == http.MethodGet {
					response, err = s.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = s.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader("{}"), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, test.limits)
				}
				if test.wantError != "" {
					require.ErrorContains(t, err, test.wantError)
				} else {
					require.NoError(t, err)
					require.Equal(t, test.body, string(response.body))
				}
			})
		}
	}
}

func TestEPBSProposalDecodesJSONResponseToSSZRequest(t *testing.T) {
	block := validEPBSBeaconBlock()
	data, _ := epbsProposalJSONBody(t, false, block)
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), connectionSynced: true, client: &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}, "Eth-Consensus-Version": {"gloas"}}, ContentLength: int64(len(data)), Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}}
	include := false
	proposal, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: block.Slot, RandaoReveal: block.Body.RANDAOReveal, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{}})
	require.NoError(t, err)
	require.Equal(t, block.Slot, proposal.Data.Gloas.Slot)
}

func TestEBPSEndpointsRejectDeclaredOversizedBodies(t *testing.T) {
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
				body := &responseLimitBody{}
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), connectionActive: true, connectionSynced: true, client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, ContentLength: maxEPBSResponseSize + 1, Body: body}, nil
				})}}
				require.ErrorContains(t, test.call(s), "response body exceeds 67108864 bytes")
				require.False(t, body.read)
			})
		}
	}
}

func TestEPBSProposalUsesSSZResponseLimitForJSONRequest(t *testing.T) {
	body := &responseLimitBody{}
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), connectionSynced: true, enforceJSON: true, client: &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "application/json", req.Header.Get("Accept"))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/octet-stream"}}, ContentLength: maxEPBSProposalResponseSize + 1, Body: body}, nil
	})}}
	include := false
	_, err = s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{}})
	require.ErrorContains(t, err, "response body exceeds 268435456 bytes")
	require.False(t, body.read)
}
