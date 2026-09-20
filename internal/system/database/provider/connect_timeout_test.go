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

package provider

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// silentServer accepts a connection and then says nothing.
//
// This is the failure that no context can end. The dial succeeds, so the
// deadline of the caller never applies, and lib/pq reads the startup handshake
// without a deadline of its own. Only connect_timeout ends it.
func silentServer(t *testing.T) (host string, port int) {

	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Hold the connection open and answer nothing.
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	addr := listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

// unreachableDataSource points the runtime at a server that never answers.
func unreachableDataSource(host string, port, timeoutSeconds int) config.Config {

	return config.Config{
		DataSource: config.DataSourceConfig{
			Type:     "postgres",
			Hostname: host,
			Port:     port,
			Username: "cdsuser",
			Password: "cdspwd",
			Name:     "cdsdb",
			SSLMode:  "disable",
			Postgres: config.PostgresConfig{ConnectTimeoutSeconds: timeoutSeconds},
		},
	}
}

// Test_getPostgresDB_failsWithinTheConnectTimeout is the acceptance criterion:
// a database that cannot be reached must fail the start within a known time.
//
// Without connect_timeout this call does not return at all, because the driver
// waits for the operating system rather than for any deadline.
func Test_getPostgresDB_failsWithinTheConnectTimeout(t *testing.T) {

	host, port := silentServer(t)
	config.OverrideCDSRuntime(unreachableDataSource(host, port, 1))
	isolatePools(t)

	start := time.Now()
	_, err := getPostgresDB()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a server that never answers")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("the attempt took %v, so the connect timeout did not end it", elapsed)
	}
}

// Test_getPostgresDB_publishesNoHandleOnFailure checks that a pool nobody could
// reach does not become the handle that every later call returns.
func Test_getPostgresDB_publishesNoHandleOnFailure(t *testing.T) {

	host, port := silentServer(t)
	config.OverrideCDSRuntime(unreachableDataSource(host, port, 1))
	isolatePools(t)

	if _, err := getPostgresDB(); err == nil {
		t.Fatal("expected an error from a server that never answers")
	}

	dbMu.Lock()
	published := postgresHandle
	dbMu.Unlock()

	if published != nil {
		t.Error("expected no handle after a failed attempt")
	}
}

// Test_getPostgresDB_triesAgainAfterAFailure checks that a failure is not
// cached. A database that is briefly unreachable at start must not break the
// instance for the life of the process.
func Test_getPostgresDB_triesAgainAfterAFailure(t *testing.T) {

	host, port := silentServer(t)
	config.OverrideCDSRuntime(unreachableDataSource(host, port, 1))
	isolatePools(t)

	if _, err := getPostgresDB(); err == nil {
		t.Fatal("expected the first attempt to fail")
	}

	// Move to a port that refuses the connection. The second attempt must
	// reach the new address, which it can do only if it built a fresh pool.
	config.OverrideCDSRuntime(unreachableDataSource("127.0.0.1", refusedPort(t), 1))

	_, err := getPostgresDB()
	if err == nil {
		t.Fatal("expected the second attempt to fail as well")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("expected the second attempt to reach the new address, got %v", err)
	}
}

// refusedPort returns a port that nothing listens on.
func refusedPort(t *testing.T) int {

	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func Test_resolveConnectTimeout(t *testing.T) {

	tests := []struct {
		name  string
		given config.PostgresConfig
		want  time.Duration
	}{
		{"empty takes the default", config.PostgresConfig{}, database.DefaultPostgresConnectTimeout},
		{"a set value is kept", config.PostgresConfig{ConnectTimeoutSeconds: 3}, 3 * time.Second},
		{"a negative value takes the default", config.PostgresConfig{ConnectTimeoutSeconds: -1},
			database.DefaultPostgresConnectTimeout},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveConnectTimeout(test.given); got != test.want {
				t.Errorf("got %v, want %v", got, test.want)
			}
		})
	}
}

// Test_getDBConfig_carriesTheConnectTimeout checks that the value reaches the
// driver, which is the only thing that bounds the startup handshake.
func Test_getDBConfig_carriesTheConnectTimeout(t *testing.T) {

	dbConfig, err := getDBConfig(unreachableDataSource("localhost", 5432, 7))
	if err != nil {
		t.Fatal(err)
	}
	if want := "connect_timeout=" + strconv.Itoa(7); !strings.Contains(dbConfig.dsn, want) {
		t.Errorf("expected the DSN to carry %q, got %q", want, dbConfig.dsn)
	}
}
