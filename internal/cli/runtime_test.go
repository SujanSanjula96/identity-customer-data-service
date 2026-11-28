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

package cli

import (
	"net"
	"path/filepath"
	"testing"
)

func TestRuntimeDefaultsCoverTheLogLevel(t *testing.T) {
	if runtimeDefaults["LOG_LEVEL"] == "" {
		t.Error("LOG_LEVEL has no default, so a pack cannot start without one being supplied")
	}
}

// start refuses an address something else holds, so the operator reads why
// rather than a bind error from deep in the server's startup.
func TestAddressInUseIsDetected(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not take a port: %v", err)
	}
	defer func() { _ = listener.Close() }()

	port := listener.Addr().(*net.TCPAddr).Port

	var config runtimeConfig
	config.Addr.Host = "127.0.0.1"
	config.Addr.Port = port
	if !config.addressInUse() {
		t.Error("a port held by another listener was reported as free")
	}

	// And the same address once it is released.
	_ = listener.Close()
	var freed runtimeConfig
	freed.Addr.Host = "127.0.0.1"
	freed.Addr.Port = port
	if freed.addressInUse() {
		t.Error("a released port was reported as in use")
	}
}

// The CLI looks for the certificate where the runtime does, or the default
// certificate warning would be about a file the server never reads.
func TestCertificatePathsResolveAsTheRuntimeDoes(t *testing.T) {
	home := "/opt/cdd"
	cases := []struct {
		name    string
		certDir string
		want    string
	}{
		{"unset", "", "/opt/cdd/etc/certs/server.crt"},
		{"relative to the home, as a pack sets it", "repository/resources/security",
			"/opt/cdd/repository/resources/security/server.crt"},
		{"absolute, as a container mounts it", "/etc/certs", "/etc/certs/server.crt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var config runtimeConfig
			config.TLS.CertDir = c.certDir
			if got := config.serverCertificate(home); got != filepath.FromSlash(c.want) {
				t.Errorf("serverCertificate = %q, want %q", got, c.want)
			}
		})
	}
}

// start refuses a sync port something else holds, and checks none without a
// sync port of its own.
func TestSyncAddressInUseIsDetected(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not take a port: %v", err)
	}
	defer func() { _ = listener.Close() }()

	var config runtimeConfig
	if config.syncAddressInUse() {
		t.Error("no sync port was reported as in use")
	}
	config.Sync.Addr.Host = "127.0.0.1"
	config.Sync.Addr.Port = listener.Addr().(*net.TCPAddr).Port
	if !config.syncAddressInUse() {
		t.Error("a sync port held by another listener was reported as free")
	}
}

// The client trust store is warned about only where the sync port requires a
// client certificate, as the runtime reads it only then.
func TestClientTrustStoreOnlyWhereItIsRequired(t *testing.T) {
	home := t.TempDir()
	var config runtimeConfig
	config.TLS.CertDir = "certs"
	config.Sync.ClientTrustStore = "client_trust_store.pem"

	if config.clientTrustStore(home) != "" {
		t.Error("a client trust store was named without a sync port")
	}
	config.Sync.Addr.Port = 8901
	if config.clientTrustStore(home) != "" {
		t.Error("a client trust store was named while no client certificate is required")
	}
	config.Sync.RequireClientCert = true
	if got, want := config.clientTrustStore(home), filepath.Join(home, "certs", "client_trust_store.pem"); got != want {
		t.Errorf("client trust store at %s, want %s", got, want)
	}
}

// A sync.addr equal to addr is no port of its own: the runtime serves the sync
// endpoints on addr, so start checks no second port.
func TestSyncAddressEqualToAddrIsNotASyncPort(t *testing.T) {
	var config runtimeConfig
	config.Addr.Host, config.Addr.Port = "127.0.0.1", 8900
	config.Sync.Addr.Host, config.Sync.Addr.Port = "127.0.0.1", 8900
	config.Sync.RequireClientCert = true
	config.Sync.ClientTrustStore = "client_trust_store.pem"

	if config.syncSeparate() {
		t.Error("sync.addr equal to addr was taken for a port of its own")
	}
	if config.clientTrustStore(t.TempDir()) != "" {
		t.Error("a client trust store was named without a sync port of its own")
	}
}
