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

package cli

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"

	"github.com/wso2/identity-customer-data-service/internal/system/certs"
)

// usingDefaultCertificate reports whether the certificate at path is the one
// the pack shipped with.
//
// That certificate is a convenience for getting started and nothing more: its
// private key ships in every Identity Server 7.3.0 download, so anyone holding
// it can present themselves as this service. An operator has to be able to
// see that it is still in use.
func usingDefaultCertificate(path string) bool {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return false
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return false
	}
	return isDefaultCertificate(block)
}

// trustsDefaultCertificate reports whether the PEM bundle at path holds the
// certificate the pack shipped with. In the client trust store it means that
// anyone holding the public default key can call the sync endpoints.
func trustsDefaultCertificate(path string) bool {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return false
	}
	for block, rest := pem.Decode(raw); block != nil; block, rest = pem.Decode(rest) {
		if isDefaultCertificate(block) {
			return true
		}
	}
	return false
}

func isDefaultCertificate(block *pem.Block) bool {
	if block.Type != "CERTIFICATE" {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	return certs.IsDefault(cert)
}
