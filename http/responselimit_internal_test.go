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

type responseLimitTransport func(*http.Request) (*http.Response, error)

func (f responseLimitTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type countedBody struct {
	io.Reader
	reads int
}

func (b *countedBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (*countedBody) Close() error                 { return nil }

func TestDeclaredResponseLengthRejectedBeforeRead(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			body := &countedBody{Reader: strings.NewReader(`{}`)}
			base, err := url.Parse("http://example.test")
			require.NoError(t, err)
			service := &Service{base: base, client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: 5, Body: body}, nil
			})}, timeout: time.Second, log: zerolog.Nop()}
			if method == http.MethodGet {
				_, err = service.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, responseLimits{ssz: 4, json: 4})
			} else {
				_, err = service.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader("{}"), ContentTypeJSON, nil, responseLimits{ssz: 4, json: 4})
			}
			require.ErrorContains(t, err, "response body exceeds 4 bytes")
			require.Zero(t, body.reads)
		})
	}
}

func TestResponseLimitUsesReturnedEncoding(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			base, err := url.Parse("http://example.test")
			require.NoError(t, err)
			service := &Service{base: base, client: &http.Client{Transport: responseLimitTransport(func(r *http.Request) (*http.Response, error) {
				require.Contains(t, r.Header.Get("Accept"), "application/octet-stream")
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: -1, Body: io.NopCloser(strings.NewReader("{}   "))}, nil
			})}, timeout: time.Second, log: zerolog.Nop()}
			limits := responseLimits{ssz: 4, json: 8}
			if method == http.MethodGet {
				_, err = service.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, limits)
			} else {
				_, err = service.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader("{}"), ContentTypeJSON, map[string]string{"Accept": "application/octet-stream"}, limits)
			}
			require.NoError(t, err)
		})
	}
}

func TestResponseLimitsByEncoding(t *testing.T) {
	tests := []struct {
		name         string
		contentTypes []string
		body         string
		limits       responseLimits
		length       int64
		err          string
	}{
		{name: "JSONAtLimit", contentTypes: []string{"application/json; charset=utf-8"}, body: "{}   ", limits: responseLimits{ssz: 3, json: 5}, length: 5},
		{name: "JSONOverLimit", contentTypes: []string{"application/json"}, body: "{}    ", limits: responseLimits{ssz: 3, json: 5}, length: -1, err: "response body exceeds 5 bytes"},
		{name: "SSZAtLimit", contentTypes: []string{"application/octet-stream"}, body: "123", limits: responseLimits{ssz: 3, json: 5}, length: 3},
		{name: "SSZOverLimit", contentTypes: []string{"application/octet-stream"}, body: "1234", limits: responseLimits{ssz: 3, json: 5}, length: -1, err: "response body exceeds 3 bytes"},
		{name: "MissingContentType", body: "{}   ", limits: responseLimits{ssz: 3, json: 5}, length: -1},
		{name: "UnknownContentType", contentTypes: []string{"text/plain"}, body: "{}   ", limits: responseLimits{ssz: 3, json: 5}, length: -1},
		{name: "MultipleContentTypes", contentTypes: []string{"application/octet-stream", "application/json"}, body: "{}   ", limits: responseLimits{ssz: 3, json: 5}, length: -1},
		{name: "UnboundedJSON", contentTypes: []string{"application/json"}, body: "{}   ", limits: responseLimits{ssz: 3}, length: 5},
		{name: "UnboundedSSZ", contentTypes: []string{"application/octet-stream"}, body: "12345", limits: responseLimits{json: 3}, length: 5},
		{name: "DeclaredSSZOverLimit", contentTypes: []string{"application/octet-stream"}, body: "123", limits: responseLimits{ssz: 3, json: 5}, length: 4, err: "response body exceeds 3 bytes"},
		{name: "DeclaredOneByteLimit", contentTypes: []string{"application/octet-stream"}, body: "1", limits: responseLimits{ssz: 1, json: 5}, length: 2, err: "response body exceeds 1 bytes"},
		{name: "DeclaredJSONOverLimit", body: "{}", limits: responseLimits{ssz: 3, json: 5}, length: 6, err: "response body exceeds 5 bytes"},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range tests {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				body := &countedBody{Reader: strings.NewReader(test.body)}
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				header := http.Header{}
				if test.contentTypes != nil {
					header["Content-Type"] = test.contentTypes
				}
				service := &Service{base: base, client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: test.length, Body: body}, nil
				})}, timeout: time.Second, log: zerolog.Nop()}
				var res *httpResponse
				if method == http.MethodGet {
					res, err = service.getWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, true, test.limits)
				} else {
					res, err = service.postWithResponseLimit(context.Background(), "/", "", &api.CommonOpts{}, strings.NewReader("{}"), ContentTypeJSON, map[string]string{"Accept": "application/octet-stream"}, test.limits)
				}
				if test.err != "" {
					require.ErrorContains(t, err, test.err)
					if strings.HasPrefix(test.name, "Declared") {
						require.Zero(t, body.reads)
					}
					return
				}
				require.NoError(t, err)
				require.Equal(t, test.body, string(res.body))
				if len(test.contentTypes) == 1 && test.contentTypes[0] == "application/octet-stream" {
					require.Equal(t, ContentTypeSSZ, res.contentType)
				} else {
					require.Equal(t, ContentTypeJSON, res.contentType)
				}
			})
		}
	}
}

func TestBoundedEndpointResponseLimits(t *testing.T) {
	included := false
	tests := []struct {
		name      string
		call      func(*Service) error
		sszLimit  int
		jsonLimit int
	}{
		{name: "EPBSProposal", sszLimit: 256 * 1024 * 1024, jsonLimit: 512 * 1024 * 1024, call: func(s *Service) error {
			_, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, BuilderConfig: &gloas.BuilderConfig{}, IncludePayload: &included})
			return err
		}},
		{name: "ExecutionPayloadEnvelope", sszLimit: 64 * 1024 * 1024, jsonLimit: 64 * 1024 * 1024, call: func(s *Service) error {
			_, err := s.ExecutionPayloadEnvelope(context.Background(), &api.ExecutionPayloadEnvelopeOpts{Slot: 1, BeaconBlockRoot: phase0.Root{1}})
			return err
		}},
		{name: "SignedExecutionPayloadEnvelope", sszLimit: 64 * 1024 * 1024, jsonLimit: 64 * 1024 * 1024, call: func(s *Service) error {
			_, err := s.SignedExecutionPayloadEnvelope(context.Background(), &api.SignedExecutionPayloadEnvelopeOpts{Block: "head"})
			return err
		}},
		{name: "PayloadAttestationData", sszLimit: 64 * 1024 * 1024, jsonLimit: 64 * 1024 * 1024, call: func(s *Service) error {
			_, err := s.PayloadAttestationData(context.Background(), &api.PayloadAttestationDataOpts{Slot: 1})
			return err
		}},
		{name: "PayloadAttestationPool", sszLimit: 64 * 1024 * 1024, jsonLimit: 64 * 1024 * 1024, call: func(s *Service) error {
			_, err := s.PayloadAttestationPool(context.Background(), &api.PayloadAttestationPoolOpts{})
			return err
		}},
	}
	for _, test := range tests {
		for _, encoding := range []string{"application/json", "application/octet-stream"} {
			t.Run(test.name+"/"+encoding, func(t *testing.T) {
				limit := test.jsonLimit
				if encoding == "application/octet-stream" {
					limit = test.sszLimit
				}
				body := &countedBody{Reader: strings.NewReader("{}")}
				base, err := url.Parse("http://example.test")
				require.NoError(t, err)
				service := &Service{base: base, connectionActive: true, connectionSynced: true, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseLimitTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {encoding}}, ContentLength: int64(limit) + 1, Body: body}, nil
				})}}
				err = test.call(service)
				require.ErrorContains(t, err, fmt.Sprintf("response body exceeds %d bytes", limit))
				require.Zero(t, body.reads)
			})
		}
	}
}

func TestEPBSProposalSSZRequestAcceptsJSONLength(t *testing.T) {
	body := &countedBody{Reader: strings.NewReader("{}")}
	base, err := url.Parse("http://example.test")
	require.NoError(t, err)
	service := &Service{base: base, connectionActive: true, connectionSynced: true, timeout: time.Second, log: zerolog.Nop(), client: &http.Client{Transport: responseLimitTransport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": {"application/json"}, "Eth-Consensus-Version": {"gloas"}},
			ContentLength: 256*1024*1024 + 1,
			Body:          body,
		}, nil
	})}}
	included := false
	_, err = service.EPBSProposal(context.Background(), &api.EPBSProposalOpts{Slot: 1, BuilderConfig: &gloas.BuilderConfig{}, IncludePayload: &included})
	require.ErrorContains(t, err, "no execution_payload_included")
	require.Positive(t, body.reads)
}
