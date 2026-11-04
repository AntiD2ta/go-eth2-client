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
	"github.com/stretchr/testify/require"
)

func TestSpecSlotDuration(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		duration time.Duration
	}{
		{name: "MissingSeconds", spec: `"SLOT_DURATION_MS":"6500"`, duration: 6500 * time.Millisecond},
		{name: "ValuesAgree", spec: `"SLOT_DURATION_MS":"12000","SECONDS_PER_SLOT":"12"`, duration: 12 * time.Second},
		{name: "ValuesDisagree", spec: `"SLOT_DURATION_MS":"6000","SECONDS_PER_SLOT":"12"`, duration: 6 * time.Second},
		{name: "LegacyOnly", spec: `"SECONDS_PER_SLOT":"12"`, duration: 12 * time.Second},
		{name: "InvalidMilliseconds", spec: `"SLOT_DURATION_MS":"invalid","SECONDS_PER_SLOT":"12"`, duration: 12 * time.Second},
		{name: "MillisecondsOverflow", spec: `"SLOT_DURATION_MS":"9223372036855","SECONDS_PER_SLOT":"12"`, duration: 12 * time.Second},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/node/version":
					fmt.Fprint(w, `{"data":{"version":"test"}}`)
				case "/eth/v1/node/syncing":
					fmt.Fprint(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
				case "/eth/v1/config/spec":
					fmt.Fprintf(w, `{"data":{%s}}`, test.spec)
				default:
					w.WriteHeader(nethttp.StatusNotFound)
				}
			}))
			defer server.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service, err := http.New(ctx, http.WithAddress(server.URL))
			require.NoError(t, err)

			response, err := service.(client.SpecProvider).Spec(ctx, &api.SpecOpts{})
			require.NoError(t, err)
			require.Equal(t, test.duration, response.Data["SECONDS_PER_SLOT"])
			duration, err := service.(client.SlotDurationProvider).SlotDuration(ctx)
			require.NoError(t, err)
			require.Equal(t, test.duration, duration)
		})
	}
}
