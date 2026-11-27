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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/wso2/identity-customer-data-service/internal/system/certs"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

// syncTLSConfig is the TLS of the sync listener, the one only the identity server calls.
//
// With sync.require_client_cert, the handshake requires a client certificate that sync.client_trust_store
// verifies, so a caller without one never reaches the endpoints. The public listener never asks for a
// client certificate, so this does not touch browsers and SDKs. A refused handshake is logged with its
// reason through the error log of the HTTP server.
func syncTLSConfig(cfg config.Config, certDir string) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if !cfg.Sync.RequireClientCert {
		return tlsConfig, nil
	}

	// Never the system CAs: any certificate a public CA issued would pass.
	if cfg.Sync.ClientTrustStore == "" {
		return nil, errors.New("sync.require_client_cert is on but sync.client_trust_store is not set")
	}
	trustPath := filepath.Join(certDir, cfg.Sync.ClientTrustStore)
	trustPEM, err := os.ReadFile(filepath.Clean(trustPath))
	if err != nil {
		return nil, fmt.Errorf("failed to read sync.client_trust_store at %s: %w", trustPath, err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(trustPEM) {
		return nil, fmt.Errorf("no certificates in sync.client_trust_store: %s", trustPath)
	}
	serverCert, _ := syncKeyPair(cfg, certDir)
	if err := checkDefaultCertificateAlone(trustPath, trustPEM, serverCert); err != nil {
		return nil, err
	}

	tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	tlsConfig.ClientCAs = clientCAs
	return tlsConfig, nil
}

// validateSync checks the sync configuration: a client certificate is requested, and a key pair of its
// own presented, on a sync listener of its own only. A sync.addr equal to addr is not one: the sync
// endpoints are then served on addr, as without sync.addr.
func validateSync(cfg config.Config) error {
	if (cfg.Sync.ServerCert == "") != (cfg.Sync.ServerKey == "") {
		return errors.New("sync.server_cert and sync.server_key must be set together")
	}
	if cfg.SyncSeparate() {
		return nil
	}
	if cfg.Sync.RequireClientCert {
		return errors.New("sync.require_client_cert needs a sync.addr of its own: a client certificate is " +
			"required on the sync port only, never on the port browsers and SDKs call")
	}
	if cfg.Sync.ServerCert != "" {
		return errors.New("sync.server_cert needs a sync.addr of its own: without one the sync endpoints are " +
			"served on addr, with tls.server_cert")
	}
	return nil
}

// syncModeGuidance says where the sync endpoints are served and how the identity server has to be set for
// that: [customer_data_service] sync_url at the sync port and server_url at addr with a sync listener of its
// own, or server_url alone at addr without one.
func syncModeGuidance(cfg config.Config) string {
	addr := net.JoinHostPort(cfg.Addr.Host, strconv.Itoa(cfg.Addr.Port))
	if cfg.SyncSeparate() {
		return fmt.Sprintf("Identity server: set sync_url to the sync port, %s, and server_url to addr, %s.",
			cfg.SyncAddress(), addr)
	}
	served := fmt.Sprintf("Sync endpoints on addr, %s.", addr)
	if cfg.Sync.Addr.Port != 0 {
		served = fmt.Sprintf("sync.addr is addr, %s: sync endpoints served beside the public APIs.", addr)
	}
	return served + " Identity server: set server_url to it, leave sync_url unset."
}

// syncKeyPair is the key pair the sync listener presents: sync.server_cert and sync.server_key, or
// tls.server_cert and tls.server_key when they are not set.
func syncKeyPair(cfg config.Config, certDir string) (string, string) {
	if cfg.Sync.ServerCert != "" {
		return filepath.Join(certDir, cfg.Sync.ServerCert), filepath.Join(certDir, cfg.Sync.ServerKey)
	}
	return filepath.Join(certDir, cfg.TLS.CDSPublicCert), filepath.Join(certDir, cfg.TLS.CDSPrivateKey)
}

// checkDefaultCertificateAlone refuses a client trust store that still trusts the default certificate the
// pack ships with once the deployment has certificates of its own: a client trust store that holds other
// certificates as well, or a server certificate that is not the default. The private key of the default is
// in every Identity Server 7.3.0 download, so anyone could present it on the sync port, which an operator
// who replaced the certificates does not expect. A pack that keeps every default starts as before.
func checkDefaultCertificateAlone(trustPath string, trustPEM []byte, serverCertPath string) error {
	trustsDefault, trustsOthers := false, false
	for _, cert := range certs.ParsePEM(trustPEM) {
		if certs.IsDefault(cert) {
			trustsDefault = true
		} else {
			trustsOthers = true
		}
	}
	if !trustsDefault {
		return nil
	}
	if trustsOthers {
		return fmt.Errorf("sync.client_trust_store %s trusts the default certificate the pack ships with besides "+
			"certificates of this deployment. Its private key is in every Identity Server 7.3.0 download: remove it",
			trustPath)
	}
	serverCerts, err := certs.ReadPEM(serverCertPath)
	if err != nil || len(serverCerts) == 0 || certs.IsDefault(serverCerts[0]) {
		return nil
	}
	return fmt.Errorf("sync.client_trust_store %s trusts the default certificate the pack ships with, while CDD "+
		"presents a certificate of its own. Its private key is in every Identity Server 7.3.0 download: replace it "+
		"with the certificate the Identity Server presents to CDD", trustPath)
}
