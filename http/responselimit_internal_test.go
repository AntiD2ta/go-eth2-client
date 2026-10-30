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
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestResponseLimitsForResponse(t *testing.T) {
	limits := responseLimits{ssz: 8, json: 16}

	tests := []struct {
		name   string
		header http.Header
		want   int
	}{
		{name: "SSZ", header: http.Header{"Content-Type": {"application/octet-stream"}}, want: 8},
		{name: "SSZWithParameter", header: http.Header{"Content-Type": {"application/octet-stream; charset=binary"}}, want: 8},
		{name: "JSON", header: http.Header{"Content-Type": {"application/json"}}, want: 16},
		{name: "Missing", header: http.Header{}, want: 16},
		{name: "Unknown", header: http.Header{"Content-Type": {"text/plain"}}, want: 16},
		{name: "Repeated", header: http.Header{"Content-Type": {"application/octet-stream", "application/json"}}, want: 16},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, limits.forResponse(test.header))
		})
	}
}

// spyBody records whether its reader was touched.
type spyBody struct {
	read bool
}

func (s *spyBody) Read(_ []byte) (int, error) {
	s.read = true

	return 0, io.EOF
}

func TestReadBoundedResponseRejectsDeclaredLengthBeforeReading(t *testing.T) {
	body := &spyBody{}
	resp := &http.Response{
		Header:        http.Header{"Content-Type": {"application/json"}},
		ContentLength: 17,
		Body:          io.NopCloser(body),
	}

	data, err := readBoundedResponse(resp, responseLimits{ssz: 8, json: 16})
	require.ErrorContains(t, err, "exceeds 16 bytes")
	require.Nil(t, data)
	require.False(t, body.read)
}

func TestResponseLimitFollowsResponseEncoding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The SSZ limit is 8 bytes and the JSON limit 16, so the same 12-byte body
	// passes as JSON and fails as SSZ.
	limits := responseLimits{ssz: 8, json: 16}

	tests := []struct {
		name        string
		contentType string
		declared    int
		body        []byte
		err         bool
	}{
		{name: "JSONUnderJSONLimit", contentType: "application/json", body: make([]byte, 12)},
		{name: "JSONAtJSONLimit", contentType: "application/json", body: make([]byte, 16)},
		{name: "JSONOverJSONLimit", contentType: "application/json", body: make([]byte, 17), err: true},
		{name: "SSZOverSSZLimit", contentType: "application/octet-stream", body: make([]byte, 12), err: true},
		{name: "SSZAtSSZLimit", contentType: "application/octet-stream", body: make([]byte, 8)},
		{name: "MissingContentTypeUsesJSONLimit", body: make([]byte, 12)},
		{name: "UnknownContentTypeUsesJSONLimit", contentType: "text/plain", body: make([]byte, 12)},
		{name: "DeclaredLengthOverLimit", contentType: "application/json", declared: 17, body: make([]byte, 17), err: true},
	}

	for _, test := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			header := w.Header()
			header.Set("Eth-Consensus-Version", "gloas")
			if test.contentType == "" {
				// A nil entry suppresses the server's content sniffing.
				header["Content-Type"] = nil
			} else {
				header.Set("Content-Type", test.contentType)
			}
			if test.declared > 0 {
				header.Set("Content-Length", strconv.Itoa(test.declared))
			}
			_, _ = w.Write(test.body)
		}))

		s := newResponseLimitTestService(t, srv.URL)

		t.Run("GET/"+test.name, func(t *testing.T) {
			res, err := s.getWithResponseLimit(ctx, "/limit", "", &api.CommonOpts{}, true, limits)
			checkResponseLimitResult(t, res != nil, err, test.err)
		})

		t.Run("POST/"+test.name, func(t *testing.T) {
			res, err := s.postWithResponseLimit(ctx, "/limit", "", &api.CommonOpts{},
				bytes.NewReader(nil), ContentTypeSSZ, nil, limits)
			checkResponseLimitResult(t, res != nil, err, test.err)
		})

		t.Cleanup(srv.Close)
	}
}

func TestZeroResponseLimitIsUnbounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Eth-Consensus-Version", "gloas")
		_, _ = w.Write(make([]byte, 1024))
	}))
	defer srv.Close()

	s := newResponseLimitTestService(t, srv.URL)

	// Only the SSZ limit is set, so the JSON encoding stays unbounded.
	res, err := s.getWithResponseLimit(ctx, "/limit", "", &api.CommonOpts{}, true, responseLimits{ssz: 8})
	require.NoError(t, err)
	require.Len(t, res.body, 1024)
}

func checkResponseLimitResult(t *testing.T, ok bool, err error, wantErr bool) {
	t.Helper()

	if wantErr {
		require.ErrorContains(t, err, "exceeds")
		require.False(t, ok)

		return
	}
	require.NoError(t, err)
	require.True(t, ok)
}

func newResponseLimitTestService(t *testing.T, address string) *Service {
	t.Helper()

	base, err := url.Parse(address)
	require.NoError(t, err)

	return &Service{
		log:     zerolog.Nop(),
		base:    base,
		address: address,
		client:  &http.Client{},
		timeout: 5 * time.Second,
	}
}
