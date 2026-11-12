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

package secrets

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testKey(t *testing.T, fill byte) string {
	t.Helper()
	key := make([]byte, KeyLength)
	for i := range key {
		key[i] = fill
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestNewSealerRefusesUnusableKeys(t *testing.T) {
	cases := map[string]string{
		"empty":        "",
		"not base64":   "not-base64-$$$",
		"wrong length": base64.StdEncoding.EncodeToString([]byte("too short")),
	}

	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSealer(key); err == nil {
				t.Fatalf("expected %s key to be refused", name)
			}
		})
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	sealer, err := NewSealer(testKey(t, 0x01))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}

	const secret = "a-client-secret-that-must-come-back-intact"
	sealed, err := sealer.Seal(secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(sealed, secret) {
		t.Fatal("the sealed value contains the plaintext")
	}

	opened, err := sealer.Open(sealed, sealer.KeyID())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened != secret {
		t.Fatalf("opened %q, want %q", opened, secret)
	}
}

// A fresh nonce per call is what keeps two organizations that happen to hold the
// same secret from being visibly identical in the database.
func TestSealIsNotDeterministic(t *testing.T) {
	sealer, err := NewSealer(testKey(t, 0x02))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}

	first, err := sealer.Seal("same-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := sealer.Seal("same-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if first == second {
		t.Fatal("sealing the same value twice produced the same ciphertext")
	}
}

// A value sealed under a key the deployment no longer holds has to say so. The
// alternative is an authentication failure, which reads like a corrupt row and
// sends the operator looking in the wrong place.
func TestOpenNamesTheKeyAValueWasSealedUnder(t *testing.T) {
	original, err := NewSealer(testKey(t, 0x03))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	sealed, err := original.Seal("secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	rotated, err := NewSealer(testKey(t, 0x04))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}

	_, err = rotated.Open(sealed, original.KeyID())
	if err == nil {
		t.Fatal("expected opening under a different key to fail")
	}
	if !strings.Contains(err.Error(), original.KeyID()) {
		t.Fatalf("error does not name the sealing key: %v", err)
	}
	if !errors.Is(err, ErrSealedUnderAnotherKey) {
		t.Fatalf("error is not ErrSealedUnderAnotherKey: %v", err)
	}
}

func TestOpenRefusesTamperedValues(t *testing.T) {
	sealer, err := NewSealer(testKey(t, 0x05))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	sealed, err := sealer.Seal("secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw[len(raw)-1] ^= 0xFF
	tampered := base64.StdEncoding.EncodeToString(raw)

	if _, err := sealer.Open(tampered, sealer.KeyID()); err == nil {
		t.Fatal("expected a tampered value to be refused")
	}
}

func TestKeyIDIsStableAndHidesTheKey(t *testing.T) {
	key := testKey(t, 0x06)
	first, err := NewSealer(key)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	second, err := NewSealer(key)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}

	if first.KeyID() != second.KeyID() {
		t.Fatalf("key IDs differ for the same key: %s and %s", first.KeyID(), second.KeyID())
	}
	if strings.Contains(key, first.KeyID()) {
		t.Fatal("the key ID appears in the key itself")
	}
}
