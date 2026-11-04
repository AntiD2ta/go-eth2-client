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

package http_test

import (
	"context"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/multi"
	"github.com/stretchr/testify/require"
)

func TestSpecSlotDuration(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		duration time.Duration
	}{
		{name: "MissingSeconds", spec: `"SLOT_DURATION_MS":"6500"`, duration: 6500 * time.Millisecond},
		{name: "ConflictingSeconds", spec: `"SECONDS_PER_SLOT":"12","SLOT_DURATION_MS":"6000"`, duration: 6 * time.Second},
		{name: "MatchingValues", spec: `"SECONDS_PER_SLOT":"12","SLOT_DURATION_MS":"12000"`, duration: 12 * time.Second},
		{name: "LegacySecondsOnly", spec: `"SECONDS_PER_SLOT":"12"`, duration: 12 * time.Second},
		{name: "InvalidMilliseconds", spec: `"SECONDS_PER_SLOT":"12","SLOT_DURATION_MS":"invalid"`, duration: 12 * time.Second},
		{name: "OverflowMilliseconds", spec: `"SECONDS_PER_SLOT":"12","SLOT_DURATION_MS":"9223372036855"`, duration: 12 * time.Second},
		{name: "LargestMilliseconds", spec: `"SLOT_DURATION_MS":"9223372036854"`, duration: 9223372036854 * time.Millisecond},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/node/syncing":
					fmt.Fprint(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false}}`)
				case "/eth/v1/node/version":
					fmt.Fprint(w, `{"data":{"version":"test"}}`)
				case "/eth/v1/config/spec":
					fmt.Fprintf(w, `{"data":{%s}}`, test.spec)
				default:
					nethttp.NotFound(w, r)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			single, err := http.New(ctx, http.WithAddress(server.URL))
			require.NoError(t, err)
			combined, err := multi.New(ctx, multi.WithClients([]client.Service{single}))
			require.NoError(t, err)

			for name, service := range map[string]client.Service{"HTTP": single, "Multi": combined} {
				t.Run(name, func(t *testing.T) {
					response, err := service.(client.SpecProvider).Spec(ctx, &api.SpecOpts{})
					require.NoError(t, err)
					require.Equal(t, test.duration, response.Data["SECONDS_PER_SLOT"])
					duration, err := service.(client.SlotDurationProvider).SlotDuration(ctx)
					require.NoError(t, err)
					require.Equal(t, test.duration, duration)
				})
			}
		})
	}
}
