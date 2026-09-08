/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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

package config

type AddrConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type LogConfig struct {
	LogLevel string `yaml:"log_level"`
}

type AuthConfig struct {
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`
}

type AuthServerConfig struct {
	Host                      string              `yaml:"host"`
	Port                      string              `yaml:"port"`
	ADUISHostname             string              `yaml:"adu_is_hostname"`
	CookieDomain              string              `yaml:"cookieDomain"`
	IntrospectionEndPoint     string              `yaml:"introspectionEndpoint"`
	TokenEndpoint             string              `yaml:"tokenEndpoint"`
	RevocationEndpoint        string              `yaml:"revocationEndpoint"`
	ClaimEndpoint             string              `yaml:"claim_endpoint"`
	ClientID                  string              `yaml:"client_id"`
	ClientSecret              string              `yaml:"client_secret"`
	IntrospectionClientId     string              `yaml:"introspection_client_id"`
	IntrospectionClientSecret string              `yaml:"introspection_client_secret"`
	AdminUsername             string              `yaml:"admin_username"`
	AdminPassword             string              `yaml:"admin_password"`
	RequiredScopes            map[string][]string `yaml:"required_scopes"`
	IsSystemAppGrantEnabled   bool                `yaml:"isSystemAppGrantEnabled"`
}

// SQLiteConfig holds the settings for the inbuilt datasource. Every field is
// optional and falls back to a default.
type SQLiteConfig struct {
	// Path is the database file location, relative to CDS_HOME unless
	// absolute.
	Path string `yaml:"path"`
	// Options is the DSN query string appended to Path. Setting it replaces
	// the defaults, so include every option that is needed.
	Options string `yaml:"options"`
	// MaxOpenConns bounds the connection pool.
	MaxOpenConns int `yaml:"max_open_conns"`
}

// PostgresConfig holds the connection pool settings for PostgreSQL. One
// instance opens one pool and keeps it for the life of the process, so these
// values bound what a single instance takes from the server. Every field is
// optional and falls back to a default.
type PostgresConfig struct {
	// MaxOpenConns bounds the connections the pool holds, in use and idle
	// together. Multiply it by the instance count, then leave headroom, when
	// you size the server's max_connections.
	MaxOpenConns int `yaml:"max_open_conns"`
	// MaxIdleConns is how many unused connections stay open. A value above
	// MaxOpenConns is lowered to it.
	MaxIdleConns int `yaml:"max_idle_conns"`
	// ConnMaxLifetimeSeconds retires a connection at this age, even a healthy
	// one, so that a failover or a DNS change takes effect.
	ConnMaxLifetimeSeconds int `yaml:"conn_max_lifetime_seconds"`
	// ConnMaxIdleTimeSeconds closes a connection that stays unused for this
	// long.
	ConnMaxIdleTimeSeconds int `yaml:"conn_max_idle_time_seconds"`
}

// DataSourceConfig selects and configures the database.
type DataSourceConfig struct {
	Type string `yaml:"type"` // "sqlite" (inbuilt, the default) or "postgres"

	// PostgreSQL settings. Read only when Type is "postgres".
	Hostname string `yaml:"hostname"`
	Port     int    `yaml:"port"`
	Name     string `yaml:"name"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	SSLMode  string `yaml:"sslmode"`

	// PostgreSQL connection pool settings. Read only when Type is "postgres".
	Postgres PostgresConfig `yaml:"postgres"`

	// SQLite settings. Read only when Type is "sqlite".
	SQLite SQLiteConfig `yaml:"sqlite"`
}

// ExternalBrokerConfig holds the connection settings that are common to
// broker-backed queue providers (e.g. any STOMP-compatible broker such as
// ActiveMQ or Artemis). Providers that need additional broker-specific
// settings should be configured via environment variables or a dedicated
// sub-section added to this struct.
type ExternalBrokerConfig struct {
	// Addr is the broker address in "host:port" format (e.g. "localhost:61613").
	Addr string `yaml:"addr"`
	// Username is the broker login name.
	Username string `yaml:"username"`
	// Password is the broker passcode.
	Password string `yaml:"password"`
	// ProfileQueueName is the destination used for profile unification
	// messages (e.g. "/queue/cds-profile-unification").
	ProfileQueueName string `yaml:"profile_queue_name"`
	// SchemaSyncQueueName is the destination used for schema sync
	// messages (e.g. "/queue/cds-schema-sync").
	SchemaSyncQueueName string `yaml:"schema_sync_queue_name"`
}

// MessageQueueConfig selects the queue provider and its settings. When Type
// is empty or "memory" the built-in in-memory queue is used. Set Type to the
// name of any registered provider (e.g. "activemq") to use an external broker.
// See docs/extending-queue-providers.md for how to add a custom provider.
type MessageQueueConfig struct {
	// Type is the queue provider to use: "memory" (default) or the name of
	// any registered external provider (e.g. "activemq").
	Type   string               `yaml:"type"`
	Broker ExternalBrokerConfig `yaml:"broker"`
}

// Application identifier types.
const (
	ApplicationIdentifierTypeClientID = "client_id"
	ApplicationIdentifierTypeAppID    = "app_id"
)

type Config struct {
	Addr         AddrConfig         `yaml:"addr"`
	ServerURL    string             `yaml:"server_url"`
	Log          LogConfig          `yaml:"log"`
	Auth         AuthConfig         `yaml:"auth"`
	AuthServer   AuthServerConfig   `yaml:"auth_server"`
	DataSource   DataSourceConfig   `yaml:"datasource"`
	TLS          TLSConfig          `yaml:"tls"`
	Cleanup      CleanupConfig      `yaml:"cleanup"`
	MessageQueue MessageQueueConfig `yaml:"message_queue"`
	// ApplicationIdentifierType selects how applications are identified: "client_id" (default) or "app_id".
	ApplicationIdentifierType string `yaml:"application_identifier_type"`
}

// UsesAppIDIdentifier reports whether applications are identified by the app ID.
func (c Config) UsesAppIDIdentifier() bool {
	return c.ApplicationIdentifierType == ApplicationIdentifierTypeAppID
}

type TLSConfig struct {
	MTLSEnabled             bool   `yaml:"mtls_enabled"`
	CertDir                 string `yaml:"cert_dir"`
	CDSPublicCert           string `yaml:"server_cert"`
	CDSPrivateKey           string `yaml:"server_key"`
	IdentityServerPublicKey string `yaml:"client_cert"`
	TrustStore              string `yaml:"trust_store"`
}

type CleanupConfig struct {
	Cookie CookieCleanupConfig `yaml:"cookie"`
}

type CookieCleanupConfig struct {
	Enabled   bool `yaml:"enabled"`
	Interval  int  `yaml:"interval"` // in seconds
	BatchSize int  `yaml:"batch_size"`
}
