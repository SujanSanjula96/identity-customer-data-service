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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// defaultCertificate is the certificate the pack ships: Identity Server 7.3.0's
// default, public part only.
var defaultCertificate = filepath.Join("testdata", "is-7.3.0-default.crt")

// Its private key is in every Identity Server download, so the runtime has to
// be able to say it is still in use — which depends on the fingerprint
// distribution/default-cert.sh pins.
func TestUsingDefaultCertificate(t *testing.T) {
	dir := t.TempDir()

	if !usingDefaultCertificate(defaultCertificate) {
		t.Error("the certificate the pack ships was not recognized as the default")
	}

	// The same subject on another key is not the default: only the fingerprint counts.
	lookalike := filepath.Join(dir, "lookalike.crt")
	writeTestCertificate(t, lookalike, "localhost")
	if usingDefaultCertificate(lookalike) {
		t.Error("a certificate with the default's subject was reported as the default")
	}

	// A path with nothing at it, and a file that is not a certificate, are
	// both "not the default" rather than errors: the caller is deciding
	// whether to warn, not whether CDD can start.
	if usingDefaultCertificate(filepath.Join(dir, "absent.crt")) {
		t.Error("a missing certificate was reported as the default")
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.crt"), []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("could not stage the file: %v", err)
	}
	if usingDefaultCertificate(filepath.Join(dir, "junk.crt")) {
		t.Error("an unreadable file was reported as the default")
	}
}

// A client trust store holding the default anywhere in its bundle lets anyone
// with the public default key call the sync endpoints, so it is found there too.
func TestTrustsDefaultCertificate(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other.crt")
	writeTestCertificate(t, other, "is.example.com")
	otherPEM, err := os.ReadFile(filepath.Clean(other))
	if err != nil {
		t.Fatalf("could not read the certificate: %v", err)
	}
	defaultPEM, err := os.ReadFile(filepath.Clean(defaultCertificate))
	if err != nil {
		t.Fatalf("could not read the default certificate: %v", err)
	}
	bundle := filepath.Clean(filepath.Join(dir, "bundle.pem"))
	//nolint:gosec // the bundle is written to the test's own temporary directory
	if err := os.WriteFile(bundle, append(otherPEM, defaultPEM...), 0o600); err != nil {
		t.Fatalf("could not stage the bundle: %v", err)
	}

	if !trustsDefaultCertificate(bundle) {
		t.Error("the default after another certificate was not found")
	}
	if trustsDefaultCertificate(other) {
		t.Error("a bundle without the default was reported as trusting it")
	}
	if trustsDefaultCertificate("") {
		t.Error("no client trust store was reported as trusting the default")
	}
}

func writeTestCertificate(t *testing.T, path, host string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("could not generate a key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host, Organization: []string{"CDD"}},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("could not create the certificate: %v", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("could not write the certificate: %v", err)
	}
}
