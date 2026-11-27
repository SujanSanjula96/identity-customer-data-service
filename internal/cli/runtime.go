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
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v2"
)

// loopback is where a server listening on every address answers locally.
const loopback = "127.0.0.1"

// runtimeConfig is the part of repository/conf/deployment.yaml the CLI needs:
// where to reach the running server, and which Identity Server the runtime is
// configured to call. The runtime itself loads the whole file; the CLI reads
// only these fields so that an unresolved ${PLACEHOLDER} elsewhere in the file
// — normal before the first start — cannot stop `start` from checking the address.
type runtimeConfig struct {
	Addr struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"addr"`
	Sync struct {
		Addr struct {
			Host string `yaml:"host"`
			Port int    `yaml:"port"`
		} `yaml:"addr"`
		RequireClientCert bool   `yaml:"require_client_cert"`
		ClientTrustStore  string `yaml:"client_trust_store"`
	} `yaml:"sync"`
	ServerURL string `yaml:"server_url"`
	TLS       struct {
		CertDir    string `yaml:"cert_dir"`
		ServerCert string `yaml:"server_cert"`
		TrustStore string `yaml:"trust_store"`
	} `yaml:"tls"`
	AuthServer struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"auth_server"`
}

func readRuntimeConfig(cdsHome string) (*runtimeConfig, error) {
	raw, err := os.ReadFile(path(cdsHome, configFile))
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", configFile, err)
	}

	var cfg runtimeConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("could not parse %s: %w", configFile, err)
	}
	if cfg.Addr.Port == 0 {
		return nil, fmt.Errorf("%s sets no addr.port, so the CDD endpoint is unknown", configFile)
	}
	if cfg.Addr.Host == "" {
		cfg.Addr.Host = loopback
	}
	if cfg.Sync.Addr.Host == "" {
		cfg.Sync.Addr.Host = cfg.Addr.Host
	}
	return &cfg, nil
}

// serverCertificate is the path of the certificate CDD presents, as the
// configuration names it.
func (c *runtimeConfig) serverCertificate(cdsHome string) string {
	name := c.TLS.ServerCert
	if name == "" {
		name = "server.crt"
	}
	return filepath.Join(c.certDir(cdsHome), name)
}

// clientTrustStore is the path of the client certificates the sync port
// accepts, or "" when it requires none.
func (c *runtimeConfig) clientTrustStore(cdsHome string) string {
	if !c.syncSeparate() || !c.Sync.RequireClientCert || c.Sync.ClientTrustStore == "" {
		return ""
	}
	return filepath.Join(c.certDir(cdsHome), c.Sync.ClientTrustStore)
}

// certDir resolves tls.cert_dir as the runtime does: relative to the CDD home,
// and etc/certs under it when unset.
func (c *runtimeConfig) certDir(cdsHome string) string {
	dir := c.TLS.CertDir
	if dir == "" {
		return path(cdsHome, "etc/certs")
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(cdsHome, dir)
}

// localBase is the address the CLI itself calls. It is always the configured
// bind address rather than server_url, because server_url is the public name
// an operator may have pointed at a load balancer — a liveness check has to
// reach this process, not the deployment.
func (c *runtimeConfig) localBase() string {
	host := c.Addr.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = loopback
	}
	return "https://" + net.JoinHostPort(host, strconv.Itoa(c.Addr.Port))
}

// addressInUse reports whether something is already listening where CDD is
// configured to listen.
//
// Checked before starting rather than left to the runtime, because the failure
// is otherwise ambiguous in the worst way: the process that cannot bind exits,
// while whatever already holds the port answers the readiness probe, and the
// start looks like it worked.
func (c *runtimeConfig) addressInUse() bool {
	return listening(c.Addr.Host, c.Addr.Port)
}

// syncAddressInUse reports whether something is already listening where CDD
// is configured to serve the sync endpoints on a port of their own.
func (c *runtimeConfig) syncAddressInUse() bool {
	return c.syncSeparate() && listening(c.Sync.Addr.Host, c.Sync.Addr.Port)
}

// syncSeparate reports whether the sync endpoints have a port of their own, as
// the runtime decides it: sync.addr names a port, and an address other than
// addr.
func (c *runtimeConfig) syncSeparate() bool {
	return c.Sync.Addr.Port != 0 && (c.Sync.Addr.Host != c.Addr.Host || c.Sync.Addr.Port != c.Addr.Port)
}

// syncAddress is where CDD is configured to serve the sync endpoints.
func (c *runtimeConfig) syncAddress() string {
	return net.JoinHostPort(c.Sync.Addr.Host, strconv.Itoa(c.Sync.Addr.Port))
}

func listening(host string, port int) bool {
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = loopback
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
