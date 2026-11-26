/*
 * Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
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

package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

// newCertificate returns a self-signed key pair with the given extended key usages, and its certificate in PEM.
func newCertificate(t *testing.T, name string, usages ...x509.ExtKeyUsage) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usages,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// defaultCertificatePEM is the certificate the pack ships: Identity Server 7.3.0's default, public part only.
func defaultCertificatePEM(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", "cli", "testdata", "is-7.3.0-default.crt")))
	require.NoError(t, err)
	return raw
}

func separate(port int) config.SyncConfig {
	return config.SyncConfig{Addr: config.AddrConfig{Port: port}}
}

func TestValidateSync(t *testing.T) {
	addr := config.AddrConfig{Host: "127.0.0.1", Port: 8900}
	required := func(port int) config.SyncConfig {
		return config.SyncConfig{Addr: config.AddrConfig{Port: port}, RequireClientCert: true}
	}
	ownKeyPair := func(port int) config.SyncConfig {
		return config.SyncConfig{Addr: config.AddrConfig{Port: port}, ServerCert: "sync.crt", ServerKey: "sync.key"}
	}
	for name, tc := range map[string]struct {
		sync config.SyncConfig
		ok   bool
	}{
		"no sync listener":                           {ok: true},
		"a sync listener":                            {sync: separate(8901), ok: true},
		"a sync listener requiring a certificate":    {sync: required(8901), ok: true},
		"a sync listener with a key pair of its own": {sync: ownKeyPair(8901), ok: true},
		"a sync listener on another interface, same port": {
			sync: config.SyncConfig{Addr: config.AddrConfig{Host: "10.0.0.5", Port: 8900}}, ok: true,
		},
		"sync.addr equal to addr, served on addr":        {sync: separate(8900), ok: true},
		"a certificate required without a sync listener": {sync: config.SyncConfig{RequireClientCert: true}},
		"a certificate required on addr":                 {sync: required(8900)},
		"a key pair of its own on addr":                  {sync: ownKeyPair(8900)},
		"a sync certificate without its key": {
			sync: config.SyncConfig{Addr: config.AddrConfig{Port: 8901}, ServerCert: "sync.crt"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateSync(config.Config{Addr: addr, Sync: tc.sync})

			if tc.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// The start-up log says which keys of the identity server name which port: sync_url the sync port and
// server_url addr with a sync listener of its own, server_url alone without one.
func TestSyncModeGuidance(t *testing.T) {
	addr := config.AddrConfig{Host: "127.0.0.1", Port: 8900}
	for name, tc := range map[string]struct {
		sync config.SyncConfig
		want string
	}{
		"no sync block": {
			want: "Sync endpoints on addr, 127.0.0.1:8900. Identity server: set server_url to it, leave " +
				"sync_url unset.",
		},
		"sync.addr equal to addr": {
			sync: separate(8900),
			want: "sync.addr is addr, 127.0.0.1:8900: sync endpoints served beside the public APIs. Identity " +
				"server: set server_url to it, leave sync_url unset.",
		},
		"a sync listener of its own": {
			sync: separate(8901),
			want: "Identity server: set sync_url to the sync port, 127.0.0.1:8901, and server_url to addr, " +
				"127.0.0.1:8900.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, syncModeGuidance(config.Config{Addr: addr, Sync: tc.sync}))
		})
	}
}

func TestSyncKeyPair(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{TLS: config.TLSConfig{CDSPublicCert: "server.crt", CDSPrivateKey: "server.key"}}
	cert, key := syncKeyPair(cfg, dir)
	assert.Equal(t, filepath.Join(dir, "server.crt"), cert, "tls.server_cert by default")
	assert.Equal(t, filepath.Join(dir, "server.key"), key)

	cfg.Sync.ServerCert, cfg.Sync.ServerKey = "sync.crt", "sync.key"
	cert, key = syncKeyPair(cfg, dir)
	assert.Equal(t, filepath.Join(dir, "sync.crt"), cert)
	assert.Equal(t, filepath.Join(dir, "sync.key"), key)
}

func TestSyncTLSConfigWithoutRequireClientCertAsksForNoClientCertificate(t *testing.T) {
	tlsConfig, err := syncTLSConfig(config.Config{Sync: separate(8901)}, t.TempDir())

	require.NoError(t, err)
	assert.Equal(t, tls.NoClientCert, tlsConfig.ClientAuth)
	assert.Equal(t, uint16(tls.VersionTLS12), tlsConfig.MinVersion)
}

func TestSyncTLSConfigRefusesRequireClientCertWithoutAClientTrustStore(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.pem"), []byte("not a certificate"), 0o600))

	for name, store := range map[string]string{"none": "", "missing": "missing.pem", "empty": "bad.pem"} {
		t.Run(name, func(t *testing.T) {
			_, err := syncTLSConfig(config.Config{Sync: config.SyncConfig{Addr: config.AddrConfig{Port: 8901},
				RequireClientCert: true, ClientTrustStore: store}}, dir)

			assert.Error(t, err)
		})
	}
}

// TestSyncTLSConfigRequiresATrustedClientCertificate runs real handshakes against the sync listener's TLS:
// only a certificate the client trust store names, for TLS client authentication, gets through.
func TestSyncTLSConfigRequiresATrustedClientCertificate(t *testing.T) {
	trusted, trustedPEM := newCertificate(t, "identity-server", x509.ExtKeyUsageClientAuth)
	untrusted, _ := newCertificate(t, "someone-else", x509.ExtKeyUsageClientAuth)
	serverOnly, serverOnlyPEM := newCertificate(t, "bought-from-a-public-ca", x509.ExtKeyUsageServerAuth)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "client_trust_store.pem"),
		append(trustedPEM, serverOnlyPEM...), 0o600))

	tlsConfig, err := syncTLSConfig(config.Config{Sync: config.SyncConfig{Addr: config.AddrConfig{Port: 8901},
		RequireClientCert: true, ClientTrustStore: "client_trust_store.pem"}}, dir)
	require.NoError(t, err)
	assert.Equal(t, tls.RequireAndVerifyClientCert, tlsConfig.ClientAuth)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = tlsConfig
	server.StartTLS()
	t.Cleanup(server.Close)

	// Sends the certificate whatever CAs the server names, as a Java client does.
	call := func(certificates ...tls.Certificate) error {
		transport := server.Client().Transport.(*http.Transport).Clone()
		transport.TLSClientConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			if len(certificates) == 0 {
				return &tls.Certificate{}, nil
			}
			return &certificates[0], nil
		}
		resp, err := (&http.Client{Transport: transport}).Get(server.URL)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}

	assert.NoError(t, call(trusted), "the trusted certificate")
	assert.Error(t, call(), "no certificate")
	assert.Error(t, call(untrusted), "a certificate the client trust store does not name")
	assert.Error(t, call(serverOnly), "a certificate for TLS server authentication only, although trusted")
}

// The default certificate's private key is in every Identity Server download. A pack that keeps every default
// starts as before; one whose certificates were replaced must not still trust the default on the sync port.
func TestSyncTLSConfigTrustsTheDefaultCertificateOnlyWhileEverythingIsDefault(t *testing.T) {
	defaultPEM := defaultCertificatePEM(t)
	_, ownPEM := newCertificate(t, "is.example.com", x509.ExtKeyUsageClientAuth)

	for name, tc := range map[string]struct {
		clientTrustStore []byte
		serverCert       []byte
		syncCert         []byte
		refused          bool
	}{
		"every default, as a first installation": {clientTrustStore: defaultPEM, serverCert: defaultPEM},
		"replaced on both sides":                 {clientTrustStore: ownPEM, serverCert: ownPEM},
		"the client trust store replaced only":   {clientTrustStore: ownPEM, serverCert: defaultPEM},
		"the default besides a certificate of the deployment": {
			clientTrustStore: append(append([]byte{}, ownPEM...), defaultPEM...), serverCert: defaultPEM, refused: true,
		},
		"the default while CDD presents its own certificate": {
			clientTrustStore: defaultPEM, serverCert: ownPEM, refused: true,
		},
		"the default while the sync port presents a certificate of its own": {
			clientTrustStore: defaultPEM, serverCert: defaultPEM, syncCert: ownPEM, refused: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "client_trust_store.pem"), tc.clientTrustStore, 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "server.crt"), tc.serverCert, 0o600))
			cfg := config.Config{
				TLS: config.TLSConfig{CDSPublicCert: "server.crt"},
				Sync: config.SyncConfig{Addr: config.AddrConfig{Port: 8901}, RequireClientCert: true,
					ClientTrustStore: "client_trust_store.pem"},
			}
			if tc.syncCert != nil {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "sync.crt"), tc.syncCert, 0o600))
				cfg.Sync.ServerCert, cfg.Sync.ServerKey = "sync.crt", "sync.key"
			}

			_, err := syncTLSConfig(cfg, dir)

			if tc.refused {
				assert.ErrorContains(t, err, "trusts the default certificate the pack ships with")
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestShutdownServersStopsBoth(t *testing.T) {
	public := httptest.NewServer(http.NotFoundHandler())
	sync := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(public.Close)
	t.Cleanup(sync.Close)

	require.NoError(t, shutdownServers(public.Config, sync.Config)(context.Background()))

	for _, server := range []*httptest.Server{public, sync} {
		_, err := http.Get(server.URL) // #nosec G107 -- the URL of a test server.
		assert.Error(t, err)
	}
}
