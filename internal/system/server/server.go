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

// Package server runs the CDS runtime: it loads the configuration, starts the
// database, the workers and the HTTPS server, and shuts them down on a signal.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	adminConfigProvider "github.com/wso2/identity-customer-data-service/internal/admin_config/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/cors"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/managers"
	_ "github.com/wso2/identity-customer-data-service/internal/system/queue/activemq" // registers the ActiveMQ queue provider
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/servicecreds"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"

	irWorker "github.com/wso2/identity-customer-data-service/internal/identity_resolution/worker"
)

func initDatabaseFromConfig(cdsConfig *config.Config) error {
	if err := provider.ValidateDataSource(cdsConfig.DataSource); err != nil {
		return err
	}

	if err := provider.EnsureDatabase(); err != nil {
		return err
	}

	ds := cdsConfig.DataSource
	if database.ResolveType(ds.Type) != database.TypeSQLite {
		log.GetLogger().Info(fmt.Sprintf("Database initialized successfully for configurations - db name:%s, "+
			"db host:%s, db port:%d", ds.Name, ds.Hostname, ds.Port))
	}

	return nil
}

// Run starts the CDS runtime in the foreground and blocks until a shutdown
// signal arrives. cdsHome is the installation root: the directory holding
// repository/, logs/ and etc/.
func Run(cdsHome string) {
	utils.SetCDSHome(cdsHome)
	const configFile = "/repository/conf/deployment.yaml"

	// Environment files are a development convenience; an installed pack has
	// none and takes its secrets from the environment, so finding none here
	// is the normal case rather than something to report.
	if envFiles, err := filepath.Glob("config/*.env"); err == nil && len(envFiles) > 0 {
		_ = godotenv.Load(envFiles...)
	}

	// Load the configuration file
	cdsConfig, err := config.LoadConfig(cdsHome, configFile)
	if err != nil {
		log.GetLogger().Error("Failed to load the configuration.", log.Error(err))
		os.Exit(1)
	}

	// Initialize runtime configurations
	if err := config.InitializeCDSRuntime(cdsHome, cdsConfig); err != nil {
		log.GetLogger().Error("Failed to initialize the CDD runtime.", log.Error(err))
		os.Exit(1)
	}

	// Initialize the application log, the access log, and the audit log.
	logStreams, err := setupLogging(cdsHome, cdsConfig.Log)
	if err != nil {
		log.GetLogger().Error("Failed to initialize logging.", log.Error(err))
		os.Exit(1)
	}
	defer logStreams.close()

	// Create the adapter of the identity provider.
	adapter, err := idp.NewAdapter(cdsConfig.IDP.Type)
	if err != nil {
		log.GetLogger().Error("Invalid identity provider configuration.", log.Error(err))
		os.Exit(1)
	}
	idp.SetAdapter(adapter)

	// A key that cannot seal the service credentials, or that the identity
	// provider has no use for, stops the server here rather than at the first
	// organization that sends credentials.
	_, credentialsSupported := adapter.(idp.ServiceCredentialsVerifier)
	if err := servicecreds.ValidateConfig(*cdsConfig, credentialsSupported); err != nil {
		log.GetLogger().Error("Invalid service credentials configuration.", log.Error(err))
		os.Exit(1)
	}
	for _, warning := range secretWarnings(*cdsConfig, credentialsSupported) {
		log.GetLogger().Warn(warning)
	}

	// The place of the sync listener is checked before anything runs.
	if err := validateSync(*cdsConfig); err != nil {
		log.GetLogger().Error("Invalid sync configuration.", log.Error(err))
		os.Exit(1)
	}

	// Resolve the shutdown deadline at start, so a refused value stops the
	// server before anything runs.
	shutdownGrace, err := config.ResolveShutdownGracePeriod(cdsConfig.Shutdown)
	if err != nil {
		log.GetLogger().Error("Invalid shutdown configuration.", log.Error(err))
		os.Exit(1)
	}
	log.GetLogger().Info(fmt.Sprintf("Shutdown grace period is %s", shutdownGrace))

	// Initialize database. This creates and initializes the inbuilt database when
	// one is configured, and must happen before the workers start since they
	// query the database.
	if err := initDatabaseFromConfig(cdsConfig); err != nil {
		log.GetLogger().Error("Failed to initialize the database.", log.Error(err))
		os.Exit(1)
	}

	if servicecreds.Enabled() {
		if err := servicecreds.CheckStore(context.Background()); err != nil {
			log.GetLogger().Error("Invalid service credentials configuration.", log.Error(err))
			os.Exit(1)
		}
	}

	// Initialize Profile worker
	if err := workers.StartProfileWorker(); err != nil {
		log.GetLogger().Error("Failed to start the profile worker.", log.Error(err))
		os.Exit(1)
	}
	workers.RegisterFuzzyResolveFunc(irWorker.ResolveProfileAsync)
	workers.RegisterReindexAfterMergeFunc(irWorker.ReindexAfterMerge)

	// Initialize Schema Sync worker
	if err := workers.StartSchemaSyncWorker(); err != nil {
		log.GetLogger().Error("Failed to start the schema sync worker.", log.Error(err))
		os.Exit(1)
	}

	// Initialize Cookie Cleanup worker
	if cdsConfig.Cleanup.Cookie.Enabled {
		workers.StartCookieCleanupWorker(cdsConfig.Cleanup.Cookie)
	}

	// Initialize the temporary profile cleanup worker
	workers.StartTemporaryProfileCleanupWorker()

	serverAddr := fmt.Sprintf("%s:%d", cdsConfig.Addr.Host, cdsConfig.Addr.Port)
	publicMux, syncMux := initMultiplexers(cdsConfig.SyncSeparate())
	mux := logStreams.wrap(newCORSMiddleware(publicMux))

	logger := log.GetLogger()
	logger.Info(fmt.Sprintf("CDD starting securely on: https://%s", serverAddr))

	certDir := cdsConfig.TLS.CertDir
	if certDir == "" {
		certDir = filepath.Join(cdsHome, "etc", "certs")
	} else if !filepath.IsAbs(certDir) {
		// Relative to the home, as a pack configures it, so the runtime finds the same files from any directory.
		certDir = filepath.Join(cdsHome, certDir)
	}

	if cdsConfig.TLS.CDSPublicCert == "" || cdsConfig.TLS.CDSPrivateKey == "" {
		logger.Error("TLS configuration is missing server certificate or key.")
		os.Exit(1)
	}

	serverCertPath := filepath.Join(certDir, cdsConfig.TLS.CDSPublicCert)
	serverKeyPath := filepath.Join(certDir, cdsConfig.TLS.CDSPrivateKey)

	// Check cert files before starting
	if _, err := os.Stat(serverCertPath); err != nil {
		if os.IsNotExist(err) {
			logger.Error(fmt.Sprintf("Server certificate not found at %s", serverCertPath))
		} else {
			logger.Error(fmt.Sprintf("Error accessing server certificate at %s: %v", serverCertPath, err))
		}
		os.Exit(1)
	}
	if _, err := os.Stat(serverKeyPath); err != nil {
		if os.IsNotExist(err) {
			logger.Error(fmt.Sprintf("Server key not found at %s", serverKeyPath))
		} else {
			logger.Error(fmt.Sprintf("Error accessing server key at %s: %v", serverKeyPath, err))
		}
		os.Exit(1)
	}

	server := newHTTPServer(serverAddr, mux, &tls.Config{
		MinVersion: tls.VersionTLS12,
	})

	// The sync endpoints on a listener of their own, which only the identity server calls.
	var syncServer *http.Server
	syncCertPath, syncKeyPath := syncKeyPair(*cdsConfig, certDir)
	if cdsConfig.SyncSeparate() {
		syncTLS, err := syncTLSConfig(*cdsConfig, certDir)
		if err != nil {
			logger.Error("Invalid sync configuration.", log.Error(err))
			os.Exit(1)
		}
		for _, file := range []string{syncCertPath, syncKeyPath} {
			if _, err := os.Stat(file); err != nil {
				logger.Error(fmt.Sprintf("Error accessing the key pair of the sync port at %s: %v", file, err))
				os.Exit(1)
			}
		}
		syncServer = newHTTPServer(cdsConfig.SyncAddress(), logStreams.wrap(security.SyncListener(syncMux)), syncTLS)
	} else {
		logger.Info(syncModeGuidance(*cdsConfig))
	}

	// Listen for OS shutdown signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start the server in a goroutine so we can listen for signals
	go func() {
		logger.Info(fmt.Sprintf("HTTPS server listening on %s", serverAddr))
		if err := server.ListenAndServeTLS(serverCertPath, serverKeyPath); err != nil && err != http.ErrServerClosed {
			logger.Error("Failed to start HTTPS server.", log.Error(err))
			os.Exit(1)
		}
	}()
	if syncServer != nil {
		go func() {
			logger.Info(fmt.Sprintf("HTTPS sync server listening on %s, client certificate required: %t",
				syncServer.Addr, cdsConfig.Sync.RequireClientCert))
			logger.Info(syncModeGuidance(*cdsConfig))
			if err := syncServer.ListenAndServeTLS(syncCertPath, syncKeyPath); err != nil &&
				err != http.ErrServerClosed {
				logger.Error("Failed to start the HTTPS sync server.", log.Error(err))
				os.Exit(1)
			}
		}()
	}

	// Block until a signal is received
	<-quit
	logger.Info("Shutdown signal received, stopping the server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	workerList := []namedWorker{
		{name: "profile", stop: workers.StopProfileWorker},
		{name: "schema sync", stop: workers.StopSchemaSyncWorker},
		{name: "cookie cleanup", stop: workers.StopCookieCleanupWorker},
		{name: "temporary profile cleanup", stop: workers.StopTemporaryProfileCleanupWorker},
	}

	if err := shutdown(ctx, logger, shutdownServers(server, syncServer), workerList, provider.CloseDB); err != nil {
		logger.Error("Shutdown completed with unfinished work.", log.Error(err))
		return
	}

	logger.Info("Shutdown complete")
}

// initMultiplexers initializes the HTTP multiplexers and registers the services: every route on the public
// mux, or, with a separate sync listener, the sync endpoints on a sync mux of their own.
func initMultiplexers(separateSync bool) (*http.ServeMux, *http.ServeMux) {
	mux := http.NewServeMux()
	var syncMux *http.ServeMux
	serviceManager := managers.NewServiceManager(mux)
	if separateSync {
		syncMux = http.NewServeMux()
		serviceManager = managers.NewSplitServiceManager(mux, syncMux)
	}
	logger := log.GetLogger()

	// Register the services.
	if err := serviceManager.RegisterServices(); err != nil {
		logger.Error("Failed to register the services. ", log.Error(err))
	}

	return mux, syncMux
}

// newHTTPServer is an HTTPS server with the timeouts and the error log every listener of CDD uses.
func newHTTPServer(addr string, handler http.Handler, tlsConfig *tls.Config) *http.Server {
	return &http.Server{
		Addr:    addr,
		Handler: handler,
		// explicit TLS settings and HTTP timeouts
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          newServerErrorLog(),
		TLSConfig:         tlsConfig,
	}
}

// shutdownServers stops the public server and the sync server, when there is one, at the same time.
func shutdownServers(server, syncServer *http.Server) func(context.Context) error {
	if syncServer == nil {
		return server.Shutdown
	}
	return func(ctx context.Context) error {
		syncErr := make(chan error, 1)
		go func() { syncErr <- syncServer.Shutdown(ctx) }()
		return errors.Join(server.Shutdown(ctx), <-syncErr)
	}
}

// newCORSMiddleware wraps handler with the organization-aware CORS middleware. Origins are
// resolved per organization from the admin configuration, falling back to the
// deployment-wide auth.cors_allowed_origins list.
func newCORSMiddleware(handler http.Handler) http.Handler {
	adminConfigService := adminConfigProvider.NewAdminConfigProvider().GetAdminConfigService()

	return cors.Middleware(handler,
		func(ctx context.Context, org cors.Org) ([]string, error) {
			// A handle CDS has no organization for has configured no origins.
			orgID, found, err := adminConfigService.LookupCORSOrgID(ctx, org.Handle)
			if err != nil || !found {
				return nil, err
			}
			return adminConfigService.GetCORSAllowedOrigins(ctx, orgID, org.SubOrgID)
		},
		func() []string { return config.GetCDSRuntime().Config.Auth.CORSAllowedOrigins },
	)
}
