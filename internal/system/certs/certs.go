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

// Package certs tells apart the certificates CDD is configured with: the default
// key pair a pack ships with, and the certificates a TLS client may present.
package certs

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
)

// defaultFingerprints are the SHA-256 fingerprints of the key pairs a pack can
// ship with: Identity Server 7.3.0's default wso2carbon pair, which
// distribution/default-cert.sh installs and pins. Changing one without the
// other leaves the runtime unable to tell a default certificate from a real
// one.
var defaultFingerprints = map[[sha256.Size]byte]bool{
	mustFingerprint("2BA361A032FFD4B34F7DF066DE72B61C5E836A09F7A7BD7105EB87B4094D9712"): true,
}

func mustFingerprint(hexDigest string) [sha256.Size]byte {
	var fingerprint [sha256.Size]byte
	if _, err := hex.Decode(fingerprint[:], []byte(hexDigest)); err != nil {
		panic(err)
	}
	return fingerprint
}

// IsDefault reports whether cert is the certificate a pack ships with. Its
// private key is in every Identity Server 7.3.0 download.
func IsDefault(cert *x509.Certificate) bool {
	return defaultFingerprints[sha256.Sum256(cert.Raw)]
}

// ParsePEM returns the certificates of a PEM bundle, skipping blocks that are
// not certificates.
func ParsePEM(raw []byte) []*x509.Certificate {
	var certificates []*x509.Certificate
	for block, rest := pem.Decode(raw); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "CERTIFICATE" {
			continue
		}
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
			certificates = append(certificates, cert)
		}
	}
	return certificates
}

// ReadPEM returns the certificates of the PEM bundle at path.
func ReadPEM(path string) ([]*x509.Certificate, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	return ParsePEM(raw), nil
}

// AllowsClientAuth reports whether cert may identify a TLS client: an extended
// key usage, when it has one, that names TLS client authentication, and a key
// usage, when it has one, that allows signatures. Public CAs now issue server
// certificates for TLS server authentication only, and both Go and Java refuse
// such a certificate from a client.
func AllowsClientAuth(cert *x509.Certificate) bool {
	if cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return false
	}
	if len(cert.ExtKeyUsage) == 0 && len(cert.UnknownExtKeyUsage) == 0 {
		return true
	}
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}
