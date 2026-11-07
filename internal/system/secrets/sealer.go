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

// Package secrets seals the credentials CDS has to be able to present again.
//
// The client secret of an Identity Server application CDS provisions for an
// organisation goes back to the token endpoint on every token request, so it
// cannot be hashed - a hash cannot be presented. It is sealed instead, under a
// key the deployment holds outside the database, so that a database dump on its
// own yields nothing that can be used against the Identity Server.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// ErrSealedUnderAnotherKey is returned by Open for a value sealed under a key
// the sealer does not hold, as after the key is changed.
var ErrSealedUnderAnotherKey = errors.New("sealed under another key")

// KeyLength is the key size a sealer takes, in bytes. AES-256-GCM is the only
// construction offered, so there is one valid length.
const KeyLength = 32

// Sealer seals and opens values under a single key.
type Sealer struct {
	keyID string
	aead  cipher.AEAD
}

// NewSealer builds a sealer from a base64-encoded key of KeyLength bytes, which
// is what `openssl rand -base64 32` prints.
func NewSealer(encodedKey string) (*Sealer, error) {

	if encodedKey == "" {
		return nil, fmt.Errorf("no encryption key is configured")
	}

	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("the encryption key is not valid base64: %w", err)
	}
	if len(key) != KeyLength {
		return nil, fmt.Errorf("the encryption key decodes to %d bytes, it has to be %d", len(key), KeyLength)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to build the cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to build the AEAD: %w", err)
	}

	return &Sealer{keyID: keyID(key), aead: aead}, nil
}

// KeyID names the key this sealer holds. It is stored beside every sealed value
// so that a value sealed under a key the deployment no longer has is reported as
// exactly that, rather than as a corrupt ciphertext.
func (s *Sealer) KeyID() string {

	return s.keyID
}

// Seal returns the sealed form of plaintext: base64 of the nonce followed by the
// ciphertext. Every call draws a fresh nonce, so sealing one value twice gives
// two different results.
func (s *Sealer) Seal(plaintext string) (string, error) {

	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to draw a nonce: %w", err)
	}

	sealed := s.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Open reverses Seal. sealedUnder is the key the value was sealed with, as it was
// stored; a value sealed under a different key is refused by name rather than
// failing authentication, because the two call for different operator action.
func (s *Sealer) Open(sealed, sealedUnder string) (string, error) {

	if sealedUnder != "" && sealedUnder != s.keyID {
		return "", fmt.Errorf("%w: the value was sealed under key %s, the configured key is %s",
			ErrSealedUnderAnotherKey, sealedUnder, s.keyID)
	}

	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("the sealed value is not valid base64: %w", err)
	}
	if len(raw) < s.aead.NonceSize() {
		return "", fmt.Errorf("the sealed value is too short to hold a nonce")
	}

	nonce, ciphertext := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to open the sealed value: %w", err)
	}

	return string(plaintext), nil
}

// keyID is a short, non-reversible name for a key: the first eight bytes of its
// SHA-256, hex encoded. It identifies a key without disclosing anything about it.
func keyID(key []byte) string {

	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}
