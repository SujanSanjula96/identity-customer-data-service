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

package certs

import (
	"crypto/x509"
	"encoding/asn1"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultCertificate is the certificate the pack ships: Identity Server 7.3.0's default, public part only.
var defaultCertificate = filepath.Join("..", "..", "cli", "testdata", "is-7.3.0-default.crt")

func readDefault(t *testing.T) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(defaultCertificate))
	require.NoError(t, err)
	certificates := ParsePEM(raw)
	require.Len(t, certificates, 1)
	return certificates[0]
}

func TestIsDefault(t *testing.T) {
	assert.True(t, IsDefault(readDefault(t)))
	assert.False(t, IsDefault(&x509.Certificate{Raw: []byte("another certificate")}))
}

// The pack's default certificate has to stay usable as a client certificate in both directions, so a first
// installation keeps working without any certificate set up.
func TestAllowsClientAuth(t *testing.T) {
	assert.True(t, AllowsClientAuth(readDefault(t)), "the default certificate")

	for name, tc := range map[string]struct {
		cert    x509.Certificate
		allowed bool
	}{
		"no usage at all": {allowed: true},
		"client authentication": {
			cert: x509.Certificate{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}, allowed: true,
		},
		"server and client authentication": {
			cert: x509.Certificate{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth,
				x509.ExtKeyUsageClientAuth}}, allowed: true,
		},
		"any usage": {cert: x509.Certificate{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}, allowed: true},
		"servers only, as public CAs now issue": {
			cert: x509.Certificate{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		},
		"an unknown usage only": {
			cert: x509.Certificate{UnknownExtKeyUsage: []asn1.ObjectIdentifier{{1, 2, 3}}},
		},
		"a key usage without signatures": {
			cert: x509.Certificate{KeyUsage: x509.KeyUsageKeyEncipherment,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.allowed, AllowsClientAuth(&tc.cert))
		})
	}
}

func TestParsePEMSkipsWhatIsNotACertificate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(defaultCertificate))
	require.NoError(t, err)
	bundle := append([]byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"), raw...)

	assert.Len(t, ParsePEM(bundle), 1)
	assert.Empty(t, ParsePEM([]byte("not a certificate")))
}
