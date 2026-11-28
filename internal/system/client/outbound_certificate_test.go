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

package client

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// writeKeyPair writes a self-signed key pair with the given extended key usages to dir as name.crt and name.key,
// and returns the certificate.
func writeKeyPair(t *testing.T, dir, name string, usages ...x509.ExtKeyUsage) []byte {
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
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".crt"),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".key"),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))
	return der
}

func presentedCertificates(client *http.Client) []tls.Certificate {
	return client.Transport.(*http.Transport).TLSClientConfig.Certificates
}

// With mtls_enabled, the server key pair is presented to the identity server only when it allows TLS client
// authentication, as the pack's default does. A certificate a public CA issued for servers only would end the
// handshake, so the calls go out without a client certificate, which the identity server does not require.
func TestOutboundClientPresentsTheServerKeyPairOnlyForClientAuthentication(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	dir := t.TempDir()
	both := writeKeyPair(t, dir, "both", x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth)
	writeKeyPair(t, dir, "server-only", x509.ExtKeyUsageServerAuth)

	httpClient, err := newOutboundHTTPClient(config.TLSConfig{CertDir: dir, MTLSEnabled: true,
		CDSPublicCert: "both.crt", CDSPrivateKey: "both.key"})
	require.NoError(t, err)
	certificates := presentedCertificates(httpClient)
	require.Len(t, certificates, 1)
	assert.Equal(t, both, certificates[0].Certificate[0])

	httpClient, err = newOutboundHTTPClient(config.TLSConfig{CertDir: dir, MTLSEnabled: true,
		CDSPublicCert: "server-only.crt", CDSPrivateKey: "server-only.key"})
	require.NoError(t, err, "a server certificate for servers only leaves the calls without a client certificate")
	assert.Empty(t, presentedCertificates(httpClient))

	httpClient, err = newOutboundHTTPClient(config.TLSConfig{CertDir: dir,
		CDSPublicCert: "both.crt", CDSPrivateKey: "both.key"})
	require.NoError(t, err)
	assert.Empty(t, presentedCertificates(httpClient), "mtls_enabled off presents none")
}
