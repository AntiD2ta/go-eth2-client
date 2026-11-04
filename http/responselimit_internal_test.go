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
	"fmt"
	"io"
	nethttp "net/http"
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

type limitRoundTripper func(*nethttp.Request) (*nethttp.Response, error)

func (f limitRoundTripper) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) { return f(req) }

type observedBody struct{ read bool }

func (b *observedBody) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }
func (*observedBody) Close() error               { return nil }

func TestResponseLimitRejectsDeclaredLengthBeforeRead(t *testing.T) {
	for _, method := range []string{nethttp.MethodGet, nethttp.MethodPost} {
		t.Run(method, func(t *testing.T) {
			body := &observedBody{}
			base, err := url.Parse("http://example.test")
			require.NoError(t, err)
			s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &nethttp.Client{Transport: limitRoundTripper(func(*nethttp.Request) (*nethttp.Response, error) {
				return &nethttp.Response{StatusCode: 200, Header: nethttp.Header{"Content-Type": {"application/json"}}, ContentLength: 5, Body: body}, nil
			})}}
			if method == nethttp.MethodGet {
				_, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, responseLimits{ssz: 4, json: 4})
			} else {
				_, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil, responseLimits{ssz: 4, json: 4})
			}
			require.ErrorContains(t, err, "response body exceeds 4 bytes")
			require.False(t, body.read)
		})
	}
}

func TestResponseLimitUsesResponseEncoding(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		limits      responseLimits
		err         string
	}{
		{name: "JSONFallback", contentType: "application/json; charset=utf-8", body: "12345678", limits: responseLimits{ssz: 4, json: 8}},
		{name: "SSZExact", contentType: "application/octet-stream", body: "1234", limits: responseLimits{ssz: 4, json: 8}},
		{name: "SSZOver", contentType: "application/octet-stream", body: "12345", limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 4 bytes"},
		{name: "JSONOver", contentType: "application/json", body: "123456789", limits: responseLimits{ssz: 4, json: 8}, err: "response body exceeds 8 bytes"},
		{name: "MissingContentType", body: "12345678", limits: responseLimits{ssz: 4, json: 8}},
		{name: "UnknownContentType", contentType: "text/plain", body: "12345678", limits: responseLimits{ssz: 4, json: 8}},
		{name: "UnboundedJSON", contentType: "application/json", body: "123456789", limits: responseLimits{ssz: 4}},
		{name: "UnboundedSSZ", contentType: "application/octet-stream", body: "123456789", limits: responseLimits{json: 4}},
	}
	for _, method := range []string{nethttp.MethodGet, nethttp.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &nethttp.Client{Transport: limitRoundTripper(func(req *nethttp.Request) (*nethttp.Response, error) {
					require.Contains(t, req.Header.Get("Accept"), "application/octet-stream")
					headers := nethttp.Header{"Eth-Consensus-Version": {"gloas"}}
					if test.contentType != "" {
						headers.Set("Content-Type", test.contentType)
					}
					return &nethttp.Response{StatusCode: 200, Header: headers, ContentLength: -1, Body: io.NopCloser(strings.NewReader(test.body))}, nil
				})}}
				var res *httpResponse
				if method == nethttp.MethodGet {
					res, err = s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
				} else {
					res, err = s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, map[string]string{"Accept": "application/octet-stream"}, test.limits)
				}
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
					return
				}
				require.NoError(t, err)
				require.Equal(t, test.body, string(res.body))
			})
		}
	}
}

type failingObservedBody struct{ read bool }

func (b *failingObservedBody) Read([]byte) (int, error) {
	b.read = true
	return 0, errors.New("body was read")
}
func (*failingObservedBody) Close() error { return nil }

func TestBoundedEndpointLimits(t *testing.T) {
	include := true
	tests := []struct {
		name string
		path string
		ssz  bool
		call func(*Service) error
	}{
		{name: "EPBSProposal", path: "/eth/v4/validator/blocks/1", ssz: true, call: func(s *Service) error {
			_, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, IncludePayload: &include, BuilderConfig: &gloas.BuilderConfig{}})
			return err
		}},
		{name: "ExecutionPayloadEnvelope", path: "/eth/v1/validator/execution_payload_envelopes/1/", ssz: true, call: func(s *Service) error {
			_, err := s.ExecutionPayloadEnvelope(context.Background(), &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})
			return err
		}},
		{name: "SignedExecutionPayloadEnvelope", path: "/eth/v1/beacon/execution_payload_envelopes/head", ssz: true, call: func(s *Service) error {
			_, err := s.SignedExecutionPayloadEnvelope(context.Background(), &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{name: "PayloadAttestationData", path: "/eth/v1/validator/payload_attestation_data", ssz: true, call: func(s *Service) error {
			_, err := s.PayloadAttestationData(context.Background(), &api.PayloadAttestationDataOpts{Slot: 1})
			return err
		}},
		{name: "PayloadAttestationPool", path: "/eth/v1/beacon/pool/payload_attestations", call: func(s *Service) error {
			_, err := s.PayloadAttestationPool(context.Background(), &api.PayloadAttestationPoolOpts{})
			return err
		}},
	}
	for _, test := range tests {
		for _, encoding := range []string{"application/json", "application/octet-stream"} {
			t.Run(test.name+"/"+encoding, func(t *testing.T) {
				limit := 64 * 1024 * 1024
				if test.name == "EPBSProposal" {
					limit = 256 * 1024 * 1024
					if encoding == "application/json" {
						limit = 512 * 1024 * 1024
					}
				}
				for _, size := range []int{limit, limit + 1} {
					body := &failingObservedBody{}
					base, err := url.Parse("http://example.test")
					require.NoError(t, err)
					s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), connectionActive: true, connectionSynced: true, client: &nethttp.Client{Transport: limitRoundTripper(func(req *nethttp.Request) (*nethttp.Response, error) {
						require.True(t, strings.HasPrefix(req.URL.Path, test.path))
						if test.ssz {
							require.Contains(t, req.Header.Get("Accept"), "application/octet-stream")
						} else {
							require.Equal(t, "application/json", req.Header.Get("Accept"))
						}
						return &nethttp.Response{StatusCode: 200, Header: nethttp.Header{"Content-Type": {encoding}}, ContentLength: int64(size), Body: body}, nil
					})}}
					err = test.call(s)
					if size > limit {
						require.ErrorContains(t, err, fmt.Sprintf("response body exceeds %d bytes", limit))
						require.False(t, body.read)
					} else {
						require.ErrorContains(t, err, "body was read")
						require.True(t, body.read)
					}
				}
			})
		}
	}
}

func TestUnboundedTransportMethods(t *testing.T) {
	for _, method := range []string{nethttp.MethodGet, nethttp.MethodPost} {
		for _, contentType := range []string{"application/json", "application/octet-stream"} {
			t.Run(method+"/"+contentType, func(t *testing.T) {
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				s := &Service{base: base, timeout: time.Second, log: zerolog.Nop(), client: &nethttp.Client{Transport: limitRoundTripper(func(*nethttp.Request) (*nethttp.Response, error) {
					return &nethttp.Response{StatusCode: 200, Header: nethttp.Header{"Content-Type": {contentType}, "Eth-Consensus-Version": {"gloas"}}, ContentLength: 8, Body: io.NopCloser(strings.NewReader("12345678"))}, nil
				})}}
				var res *httpResponse
				if method == nethttp.MethodGet {
					res, err = s.get(context.Background(), "/test", "", &api.CommonOpts{}, true)
				} else {
					res, err = s.post(context.Background(), "/test", "", &api.CommonOpts{}, strings.NewReader(""), ContentTypeSSZ, nil)
				}
				require.NoError(t, err)
				require.Equal(t, "12345678", string(res.body))
			})
		}
	}
}
