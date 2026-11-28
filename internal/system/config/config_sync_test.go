/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSyncListenerPlace(t *testing.T) {
	addr := AddrConfig{Host: "127.0.0.1", Port: 8900}
	for name, tc := range map[string]struct {
		sync     AddrConfig
		separate bool
	}{
		"no sync.addr":                      {},
		"a host without a port":             {sync: AddrConfig{Host: "10.0.0.5"}},
		"the address of addr":               {sync: AddrConfig{Host: "127.0.0.1", Port: 8900}},
		"the port of addr, on its host":     {sync: AddrConfig{Port: 8900}},
		"a port of its own":                 {sync: AddrConfig{Port: 8901}, separate: true},
		"the port of addr, on another host": {sync: AddrConfig{Host: "10.0.0.5", Port: 8900}, separate: true},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.separate, Config{Addr: addr, Sync: SyncConfig{Addr: tc.sync}}.SyncSeparate())
		})
	}

	cfg := Config{Addr: addr, Sync: SyncConfig{Addr: AddrConfig{Port: 8901}}}
	assert.Equal(t, "127.0.0.1:8901", cfg.SyncAddress(), "the host of addr")
	cfg.Sync.Addr.Host = "::1"
	assert.Equal(t, "[::1]:8901", cfg.SyncAddress())
}
