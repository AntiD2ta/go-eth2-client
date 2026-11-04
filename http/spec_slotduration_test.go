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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	eth2http "github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/multi"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestSpecSlotDuration(t *testing.T) {
	for _, test := range []struct {
		name         string
		data         string
		milliseconds any
		duration     time.Duration
	}{
		{name: "MissingSeconds", data: `{"SLOT_DURATION_MS":"6500"}`, milliseconds: uint64(6500), duration: 6500 * time.Millisecond},
		{name: "Agree", data: `{"SLOT_DURATION_MS":"12000","SECONDS_PER_SLOT":"12"}`, milliseconds: uint64(12000), duration: 12 * time.Second},
		{name: "Disagree", data: `{"SLOT_DURATION_MS":"6500","SECONDS_PER_SLOT":"12"}`, milliseconds: uint64(6500), duration: 6500 * time.Millisecond},
		{name: "LegacyOnly", data: `{"SECONDS_PER_SLOT":"12"}`, duration: 12 * time.Second},
		{name: "InvalidMilliseconds", data: `{"SLOT_DURATION_MS":"invalid","SECONDS_PER_SLOT":"12"}`, milliseconds: "invalid", duration: 12 * time.Second},
		{name: "OverflowMilliseconds", data: `{"SLOT_DURATION_MS":"9223372036855","SECONDS_PER_SLOT":"12"}`, milliseconds: uint64(9223372036855), duration: 12 * time.Second},
		{name: "MaximumMilliseconds", data: `{"SLOT_DURATION_MS":"9223372036854"}`, milliseconds: uint64(9223372036854), duration: 9223372036854 * time.Millisecond},
		{name: "ZeroMilliseconds", data: `{"SLOT_DURATION_MS":"0","SECONDS_PER_SLOT":"12"}`, milliseconds: uint64(0), duration: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			testSpecSlotDuration(t, test.data, test.milliseconds, test.duration)
		})
	}
}

func testSpecSlotDuration(t *testing.T, data string, milliseconds any, expected time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			fmt.Fprint(w, `{"data":{"version":"stub"}}`)
		case "/eth/v1/node/syncing":
			fmt.Fprint(w, `{"data":{"head_slot":"64","sync_distance":"0","is_syncing":false,"is_optimistic":false}}`)
		case "/eth/v1/config/spec":
			fmt.Fprintf(w, `{"data":%s}`, data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	multiService, err := multi.New(ctx, multi.WithClients([]client.Service{service}), multi.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)

	for _, provider := range []client.Service{service, multiService} {
		t.Run(provider.Name(), func(t *testing.T) {
			response, err := provider.(client.SpecProvider).Spec(ctx, &api.SpecOpts{})
			require.NoError(t, err)
			require.Equal(t, milliseconds, response.Data["SLOT_DURATION_MS"])
			require.Equal(t, expected, response.Data["SECONDS_PER_SLOT"])

			duration, err := provider.(client.SlotDurationProvider).SlotDuration(ctx)
			if expected == 0 && provider == multiService {
				require.ErrorContains(t, err, "zero duration not a valid response")
				return
			}
			require.NoError(t, err)
			require.Equal(t, expected, duration)
		})
	}
}
