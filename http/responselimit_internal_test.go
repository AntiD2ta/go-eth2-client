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
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/stretchr/testify/require"
)

type responseLimitTransport func(*stdhttp.Request) (*stdhttp.Response, error)

func (f responseLimitTransport) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) {
	return f(req)
}

type observedBody struct {
	read bool
}

func (b *observedBody) Read([]byte) (int, error) {
	b.read = true
	return 0, io.EOF
}

func (*observedBody) Close() error { return nil }

func responseLimitService(t *testing.T, responseForProposal func(*stdhttp.Request) *stdhttp.Response) (*Service, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	transport := responseLimitTransport(func(req *stdhttp.Request) (*stdhttp.Response, error) {
		response := &stdhttp.Response{StatusCode: stdhttp.StatusOK, Header: make(stdhttp.Header), Request: req}
		response.Header.Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/eth/v1/node/syncing":
			response.Body = io.NopCloser(strings.NewReader(`{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false}}`))
		case "/eth/v1/node/version":
			response.Body = io.NopCloser(strings.NewReader(`{"data":{"version":"test"}}`))
		default:
			return responseForProposal(req), nil
		}
		return response, nil
	})
	service, err := New(ctx, WithAddress("http://example.test"), WithHTTPClient(&stdhttp.Client{Transport: transport}))
	require.NoError(t, err)
	return service.(*Service), ctx
}

func TestEPBSProposalRejectsDeclaredOversizedSSZBeforeReading(t *testing.T) {
	body := &observedBody{}
	service, ctx := responseLimitService(t, func(req *stdhttp.Request) *stdhttp.Response {
		return &stdhttp.Response{
			StatusCode: stdhttp.StatusOK, Header: stdhttp.Header{"Content-Type": {"application/octet-stream"}},
			ContentLength: 256*1024*1024 + 1, Body: body, Request: req,
		}
	})
	include := false
	_, err := service.EPBSProposal(ctx, &api.EPBSProposalOpts{
		Slot: 1, BuilderConfig: &gloas.BuilderConfig{}, IncludePayload: &include,
	})
	require.ErrorContains(t, err, "response body exceeds 268435456 bytes")
	require.False(t, body.read)
}

func TestEPBSProposalAcceptsJSONResponseToSSZRequest(t *testing.T) {
	block := validEPBSBeaconBlock()
	body, _ := epbsProposalJSONBody(t, false, block)
	var accept string
	service, ctx := responseLimitService(t, func(req *stdhttp.Request) *stdhttp.Response {
		accept = req.Header.Get("Accept")
		return &stdhttp.Response{
			StatusCode:    stdhttp.StatusOK,
			Header:        stdhttp.Header{"Content-Type": {"application/json"}, "Eth-Consensus-Version": {"gloas"}},
			ContentLength: 256*1024*1024 + 1, Body: io.NopCloser(strings.NewReader(string(body))), Request: req,
		}
	})
	include := false
	response, err := service.EPBSProposal(ctx, &api.EPBSProposalOpts{
		Slot: block.Slot, RandaoReveal: block.Body.RANDAOReveal,
		BuilderConfig: &gloas.BuilderConfig{}, IncludePayload: &include,
	})
	require.NoError(t, err)
	require.Equal(t, "application/octet-stream", accept)
	require.Equal(t, block.Slot, response.Data.Gloas.Slot)
}
