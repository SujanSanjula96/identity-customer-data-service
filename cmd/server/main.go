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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/managers"
	_ "github.com/wso2/identity-customer-data-service/internal/system/queue/activemq" // registers the ActiveMQ queue provider
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
)

func initDatabaseFromConfig(cdsConfig *config.Config) error {

	if err := provider.ValidateDataSource(cdsConfig.DataSource); err != nil {
		return err
	}

	// This opens the shared connection pool that every request reuses. For the
	// inbuilt database it also creates the file and applies the schema.
	if err := provider.EnsureDatabase(); err != nil {
		return err
	}

	ds := cdsConfig.DataSource
	if database.ResolveType(ds.Type) == database.TypeSQLite {
		return nil
	}

	log.GetLogger().Info(fmt.Sprintf("Database initialized successfully for configurations - db name:%s, "+
		"db host:%s, db port:%d", ds.Name, ds.Hostname, ds.Port))

	return nil
}

func main() {

	cdsHome := resolveCDSHome()
	utils.SetCDSHome(cdsHome)
	const configFile = "/repository/conf/deployment.yaml"

	envFiles, err := filepath.Glob("config/*.env")
	if err != nil || len(envFiles) == 0 {
		fmt.Println("No .env files found in config directory. ", err)
	}
	_ = godotenv.Load(envFiles...)

	// Load the configuration file
	cdsConfig, err := config.LoadConfig(cdsHome, configFile)
	if err != nil {
		fmt.Println("Failed to load cdsConfig. ", err)
		os.Exit(1)
	}

	// Initialize runtime configurations
	if err := config.InitializeCDSRuntime(cdsHome, cdsConfig); err != nil {
		fmt.Println("Failed to initialize cds runtime.", err)
		os.Exit(1)
	}

	// Initialize logger
	if err := log.Init(cdsConfig.Log.LogLevel); err != nil {
		fmt.Println("Failed to initialize logger.", err)
		os.Exit(1)
	}

	// Initialize database. This creates and initializes the inbuilt database when
	// one is configured, and must happen before the workers start since they
	// query the database.
	if err := initDatabaseFromConfig(cdsConfig); err != nil {
		log.GetLogger().Error("Failed to initialize the database.", log.Error(err))
		os.Exit(1)
	}

	// Initialize Profile worker
	if err := workers.StartProfileWorker(); err != nil {
		fmt.Println("Failed to start profile worker.", err)
		os.Exit(1)
	}

	// Initialize Schema Sync worker
	if err := workers.StartSchemaSyncWorker(); err != nil {
		fmt.Println("Failed to start schema sync worker.", err)
		os.Exit(1)
	}

	// Initialize Cookie Cleanup worker
	if cdsConfig.Cleanup.Cookie.Enabled {
		workers.StartCookieCleanupWorker(cdsConfig.Cleanup.Cookie)
	}

	serverAddr := fmt.Sprintf("%s:%d", cdsConfig.Addr.Host, cdsConfig.Addr.Port)
	mux := enableCORS(initMultiplexer())

	logger := log.GetLogger()
	logger.Info(fmt.Sprintf("WSO2 CDS starting securely on: https://%s", serverAddr))

	certDir := cdsConfig.TLS.CertDir
	if certDir == "" {
		certDir = filepath.Join(cdsHome, "etc", "certs")
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

	server := &http.Server{
		Addr:    serverAddr,
		Handler: mux,
		// explicit TLS settings and HTTP timeouts
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
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

	// Block until a signal is received
	<-quit
	logger.Info("Shutdown signal received, draining connections...")

	// Give in-flight requests up to 15 seconds to complete
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("HTTP server shutdown error.", log.Error(err))
	}
	if err := workers.StopProfileWorker(); err != nil {
		logger.Error("Failed to stop profile worker.", log.Error(err))
	}
	if err := workers.StopSchemaSyncWorker(); err != nil {
		logger.Error("Failed to stop schema sync worker.", log.Error(err))
	}

	workers.StopCookieCleanupWorker()

	// The pool is shared, so it is closed here rather than by any store.
	if err := provider.CloseDB(); err != nil {
		logger.Error("Failed to close the database connections.", log.Error(err))
	}

	logger.Info("Shutdown complete")
}

// initMultiplexer initializes the HTTP multiplexer and registers the services.
func initMultiplexer() *http.ServeMux {

	mux := http.NewServeMux()
	serviceManager := managers.NewServiceManager(mux)
	logger := log.GetLogger()

	// Register the services.
	if err := serviceManager.RegisterServices(); err != nil {
		logger.Error("Failed to register the services. ", log.Error(err))
	}

	return mux
}

// enableCORS allows cross-origin requests for UI/SDK clients.
func enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdsAuthConfig := config.GetCDSRuntime().Config.Auth
		origin := r.Header.Get("Origin")

		// Check if the origin is in the allowed list
		allowedOrigin := ""
		for _, allowed := range cdsAuthConfig.CORSAllowedOrigins {
			if origin == allowed {
				allowedOrigin = origin
				break
			}
		}

		// Only set CORS headers if origin is allowed
		if allowedOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
			if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
				w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
			} else {
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			}
			w.Header().Set("Access-Control-Expose-Headers", "Content-Length")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// resolveCDSHome parses flags and determines the CDS home directory.
func resolveCDSHome() string {
	// Define the flag locally
	cdsHomeFlag := flag.String("cdsHome", "", "Path to customer data service home directory")

	// Parse flags once (only if not already parsed)
	if !flag.Parsed() {
		flag.Parse()
	}

	// Determine the directory
	if *cdsHomeFlag != "" {
		fmt.Printf("Using %s from command line argument\n", *cdsHomeFlag)
		return *cdsHomeFlag
	}

	// Fallback to environment variable
	if envHome := os.Getenv("CDS_HOME"); envHome != "" {
		fmt.Printf("Using CDS_HOME from environment: %s\n", envHome)
		return envHome
	}

	// Fallback to working directory
	dir, err := os.Getwd()
	if err != nil {
		fmt.Println("Failed to get current working directory", err)
		os.Exit(1)
	}
	return dir
}
