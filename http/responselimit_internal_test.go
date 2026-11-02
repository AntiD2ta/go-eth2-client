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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

// responseLimitService returns a service whose transport talks to handler.
func responseLimitService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	base, err := url.Parse(server.URL)
	require.NoError(t, err)

	return &Service{
		log:     zerolog.Nop(),
		base:    base,
		address: server.URL,
		client:  server.Client(),
		timeout: 5 * time.Second,
	}
}

// TestResponseLimitFollowsResponseEncoding drives GET and POST through the same
// cases: the limit comes from the encoding the node answered with, whatever was
// requested.
func TestResponseLimitFollowsResponseEncoding(t *testing.T) {
	limits := responseLimit{ssz: 4, json: 8}

	tests := []struct {
		name        string
		contentType string
		body        string
		limits      responseLimit
		// chunked streams the body without a Content-Length, so only the read can
		// enforce the limit.
		chunked bool
		err     string
	}{
		{
			name:        "JSONAnswerToSSZRequestUsesJSONLimit",
			contentType: "application/json",
			body:        "12345678",
			limits:      limits,
		},
		{
			name:        "JSONAboveJSONLimit",
			contentType: "application/json",
			body:        "123456789",
			limits:      limits,
			chunked:     true,
			err:         "response body exceeds 8 bytes",
		},
		{
			name:        "SSZAtSSZLimit",
			contentType: "application/octet-stream",
			body:        "1234",
			limits:      limits,
		},
		{
			name:        "SSZAboveSSZLimit",
			contentType: "application/octet-stream",
			body:        "12345",
			limits:      limits,
			chunked:     true,
			err:         "response body exceeds 4 bytes",
		},
		{
			name:   "NoContentTypeUsesJSONLimit",
			body:   "12345678",
			limits: limits,
		},
		{
			name:    "NoContentTypeAboveJSONLimit",
			body:    "123456789",
			limits:  limits,
			chunked: true,
			err:     "response body exceeds 8 bytes",
		},
		{
			name:        "UnknownContentTypeUsesJSONLimit",
			contentType: "text/plain",
			body:        "12345678",
			limits:      limits,
		},
		{
			name:        "DeclaredLengthAboveLimitRejectedBeforeRead",
			contentType: "application/octet-stream",
			body:        "12345",
			limits:      limits,
			err:         "declared response body of 5 bytes exceeds 4 bytes",
		},
		{
			name:        "ZeroSSZLimitIsUnbounded",
			contentType: "application/octet-stream",
			body:        strings.Repeat("1", 100),
			limits:      responseLimit{json: 8},
		},
		{
			name:        "ZeroJSONLimitIsUnbounded",
			contentType: "application/json",
			body:        strings.Repeat("1", 100),
			limits:      responseLimit{ssz: 4},
		},
	}

	for _, test := range tests {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			// Spares GET parsing a version from bodies that are only byte counts.
			w.Header().Set("Eth-Consensus-Version", "gloas")
			if test.contentType != "" {
				w.Header().Set("Content-Type", test.contentType)
			}
			if test.chunked {
				w.(http.Flusher).Flush()
			}
			_, _ = w.Write([]byte(test.body))
		}

		check := func(t *testing.T, res *httpResponse, err error) {
			t.Helper()
			if test.err != "" {
				require.ErrorContains(t, err, test.err)
				require.Nil(t, res)

				return
			}
			require.NoError(t, err)
			require.Equal(t, []byte(test.body), res.body)
		}

		t.Run("GET/"+test.name, func(t *testing.T) {
			s := responseLimitService(t, handler)
			res, err := s.getWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{}, true, test.limits)
			check(t, res, err)
		})

		t.Run("POST/"+test.name, func(t *testing.T) {
			s := responseLimitService(t, handler)
			res, err := s.postWithResponseLimit(context.Background(), "/test", "", &api.CommonOpts{},
				bytes.NewReader(nil), ContentTypeSSZ,
				map[string]string{"Accept": ContentTypeSSZ.MediaType()}, test.limits)
			check(t, res, err)
		})
	}
}

// TestEPBSProposalResponseLimitFollowsResponseEncoding asks for an SSZ proposal
// and has the node answer with a declared length, so the limit EPBSProposal
// applies shows without sending hundreds of MiB.
func TestEPBSProposalResponseLimitFollowsResponseEncoding(t *testing.T) {
	tests := []struct {
		name          string
		contentType   string
		contentLength int
		err           string
	}{
		{
			// Above the SSZ limit but within the JSON one, so it is read; the
			// truncated body then fails for want of content, not for its size.
			name:          "JSONAnswerBetweenLimits",
			contentType:   "application/json",
			contentLength: 300 * 1024 * 1024,
		},
		{
			name:          "JSONAnswerAboveJSONLimit",
			contentType:   "application/json",
			contentLength: 512*1024*1024 + 1,
			err:           "declared response body of 536870913 bytes exceeds 536870912 bytes",
		},
		{
			name:          "SSZAnswerAboveSSZLimit",
			contentType:   "application/octet-stream",
			contentLength: 256*1024*1024 + 1,
			err:           "declared response body of 268435457 bytes exceeds 268435456 bytes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var accept string
			s := responseLimitService(t, func(w http.ResponseWriter, r *http.Request) {
				accept = r.Header.Get("Accept")
				w.Header().Set("Content-Type", test.contentType)
				w.Header().Set("Content-Length", strconv.Itoa(test.contentLength))
			})
			s.connectionActive = true
			s.connectionSynced = true

			includePayload := true
			_, err := s.EPBSProposal(context.Background(), &api.EPBSProposalOpts{
				Slot:           1,
				BuilderConfig:  &gloas.BuilderConfig{Builders: []*gloas.BuilderEntry{}},
				IncludePayload: &includePayload,
			})
			require.Equal(t, ContentTypeSSZ.MediaType(), accept)
			require.Error(t, err)
			if test.err != "" {
				require.ErrorContains(t, err, test.err)
			} else {
				require.NotContains(t, err.Error(), "exceeds")
			}
		})
	}
}
