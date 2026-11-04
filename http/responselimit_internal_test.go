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
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type limitTransport func(*http.Request) (*http.Response, error)

func (f limitTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type observedBody struct{ read bool }

func (b *observedBody) Read(_ []byte) (int, error) { b.read = true; return 0, io.EOF }
func (*observedBody) Close() error                 { return nil }

func TestResponseLimitRejectsDeclaredLengthBeforeRead(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			body := &observedBody{}
			base, err := url.Parse("http://example.invalid")
			require.NoError(t, err)
			s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: limitTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: 5, Body: body}, nil
			})}}
			if method == http.MethodGet {
				_, err = s.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, responseLimits{ssz: 4, json: 4})
			} else {
				_, err = s.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, responseLimits{ssz: 4, json: 4})
			}
			require.ErrorContains(t, err, "response body exceeds 4 bytes")
			require.False(t, body.read)
		})
	}
}

func TestEPBSProposalAcceptsJSONAfterSSZRequest(t *testing.T) {
	block := validEPBSBeaconBlock()
	jsonBody, _ := epbsProposalJSONBody(t, false, block)
	base, err := url.Parse("http://example.invalid")
	require.NoError(t, err)
	var accept string
	s := &Service{
		base: base, timeout: time.Second, log: zerolog.Nop(), connectionActive: true, connectionSynced: true,
		client: &http.Client{Transport: limitTransport(func(r *http.Request) (*http.Response, error) {
			accept = r.Header.Get("Accept")
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}, "Eth-Consensus-Version": {"gloas"}},
				ContentLength: 256*1024*1024 + 1, Body: io.NopCloser(strings.NewReader(string(jsonBody))),
			}, nil
		})},
	}
	included := false
	response, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
		Slot: block.Slot, RandaoReveal: block.Body.RANDAOReveal, IncludePayload: &included,
		BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}},
	})
	require.NoError(t, err)
	require.Equal(t, "application/octet-stream", accept)
	require.Equal(t, block.Slot, response.Data.Gloas.Slot)
}

func TestResponseLimitsFollowResponseEncoding(t *testing.T) {
	tests := []struct {
		name, contentType, body string
		limits                  responseLimits
		wantError               string
	}{
		{name: "JSONOverSSZLimit", contentType: "application/json; charset=utf-8", body: "12345", limits: responseLimits{ssz: 4, json: 6}},
		{name: "JSONExactlyAtLimit", contentType: "application/json", body: "123456", limits: responseLimits{ssz: 4, json: 6}},
		{name: "JSONOverLimit", contentType: "application/json", body: "1234567", limits: responseLimits{ssz: 4, json: 6}, wantError: "response body exceeds 6 bytes"},
		{name: "SSZExactlyAtLimit", contentType: "application/octet-stream", body: "1234", limits: responseLimits{ssz: 4, json: 6}},
		{name: "SSZOverLimit", contentType: "application/octet-stream", body: "12345", limits: responseLimits{ssz: 4, json: 6}, wantError: "response body exceeds 4 bytes"},
		{name: "MissingTypeUsesJSON", body: "12345", limits: responseLimits{ssz: 4, json: 6}},
		{name: "MissingTypeOverJSONLimit", body: "1234567", limits: responseLimits{ssz: 4, json: 6}, wantError: "response body exceeds 6 bytes"},
		{name: "UnknownTypeUsesJSON", contentType: "text/plain", body: "12345", limits: responseLimits{ssz: 4, json: 6}},
		{name: "UnknownTypeOverJSONLimit", contentType: "text/plain", body: "1234567", limits: responseLimits{ssz: 4, json: 6}, wantError: "response body exceeds 6 bytes"},
		{name: "UnboundedSSZ", contentType: "application/octet-stream", body: "1234567", limits: responseLimits{json: 4}},
		{name: "UnboundedJSON", contentType: "application/json", body: "1234567", limits: responseLimits{ssz: 4}},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				base, err := url.Parse("http://example.invalid")
				require.NoError(t, err)
				s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: limitTransport(func(r *http.Request) (*http.Response, error) {
					require.Contains(t, r.Header.Get("Accept"), "application/octet-stream")
					header := http.Header{"Eth-Consensus-Version": {"gloas"}}
					if test.contentType != "" {
						header.Set("Content-Type", test.contentType)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: -1, Body: io.NopCloser(strings.NewReader(test.body))}, nil
				})}}
				var response *httpResponse
				if method == http.MethodGet {
					response, err = s.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, test.limits)
				} else {
					response, err = s.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, test.limits)
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

func TestEPBSFetchesRejectOversizedDeclaredLength(t *testing.T) {
	for _, test := range []struct {
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
	} {
		for _, contentType := range []string{"application/json", "application/octet-stream"} {
			t.Run(test.name+"/"+contentType, func(t *testing.T) {
				body := &observedBody{}
				base, err := url.Parse("http://example.invalid")
				require.NoError(t, err)
				s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), connectionActive: true, connectionSynced: true,
					client: &http.Client{Transport: limitTransport(func(*http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, ContentLength: 64*1024*1024 + 1, Body: body}, nil
					})}}
				require.ErrorContains(t, test.call(s), "response body exceeds 67108864 bytes")
				require.False(t, body.read)
			})
		}
	}
}

func TestEPBSProposalRejectsOversizedDeclaredLength(t *testing.T) {
	for _, test := range []struct {
		name, contentType string
		length            int64
		wantError         string
	}{
		{name: "SSZ", contentType: "application/octet-stream", length: 256*1024*1024 + 1, wantError: "response body exceeds 268435456 bytes"},
		{name: "JSON", contentType: "application/json", length: 512*1024*1024 + 1, wantError: "response body exceeds 536870912 bytes"},
		{name: "MissingType", length: 512*1024*1024 + 1, wantError: "response body exceeds 536870912 bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &observedBody{}
			base, err := url.Parse("http://example.invalid")
			require.NoError(t, err)
			s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), connectionActive: true, connectionSynced: true,
				client: &http.Client{Transport: limitTransport(func(r *http.Request) (*http.Response, error) {
					require.Equal(t, "application/octet-stream", r.Header.Get("Accept"))
					header := http.Header{}
					if test.contentType != "" {
						header.Set("Content-Type", test.contentType)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.length, Body: body}, nil
				})}}
			included := false
			_, err = s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, IncludePayload: &included, BuilderConfig: &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}}})
			require.ErrorContains(t, err, test.wantError)
			require.False(t, body.read)
		})
	}
}
