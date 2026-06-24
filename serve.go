package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/cloudflare/cloudflare-go"
)


//go:embed web/*
var webFS embed.FS

// RunServe starts the web setup server on the given port.
func RunServe(port int, stopChan chan struct{}) {
	// Check if target directory exists, create if not
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		log.Fatalf("Failed to ensure config directory: %v", err)
	}

	// Try loading existing config
	_ = LoadConfig()

	subFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("Failed to create sub filesystem: %v", err)
	}

	mux := http.NewServeMux()

	// Static files (the Vue setup UI)
	fileHandler := http.FileServer(http.FS(subFS))
	mux.Handle("/", fileHandler)

	// GET /api/config — returns current configuration
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		cfgMutex.RLock()
		cfg := appConfig
		cfgMutex.RUnlock()

		// Check disk images and rootfs directory
		configsOk := false
		storageOk := false
		rootfsOk := false
		if _, err := os.Stat("vms/gcore_configs.img"); err == nil {
			configsOk = true
		}
		if _, err := os.Stat("vms/gcore_storage.img"); err == nil {
			storageOk = true
		}
		if fi, err := os.Stat("vms/rootfs"); err == nil && fi.IsDir() {
			rootfsOk = true
		}

		resp := map[string]interface{}{
			"configured":             cfg.LDAPUserPass != "",
			"vm_ready":               configsOk && storageOk && rootfsOk,
			"port":                   port,
			"ldap_user_pass":         cfg.LDAPUserPass,
			"master_username":        cfg.MasterUsername,
			"org_name":               cfg.OrgName,
			"ldap_base_dn":           cfg.LDAPBaseDN,
			"ldap_port":              cfg.LDAPPort,
			"http_port":              cfg.HTTPPort,
			"cf_domain":              cfg.CFDomain,
			"cf_api_token":           cfg.CFApiToken,
			"forgejo_enabled":        cfg.ForgejoEnabled,
			"forgejo_subdomain":      cfg.ForgejoSubdomain,
			"forgejo_http_port":      cfg.ForgejoHTTPPort,
			"forgejo_ssh_port":       cfg.ForgejoSSHPort,
			"pocket_id_enabled":      cfg.PocketIDEnabled,
			"pocket_id_subdomain":    cfg.PocketIDSubdomain,
			"pocket_id_port":         cfg.PocketIDPort,
			"stalwart_enabled":       cfg.StalwartEnabled,
			"stalwart_subdomain":     cfg.StalwartSubdomain,
			"stalwart_http_port":     cfg.StalwartHTTPPort,
			"sftp_enabled":           cfg.SFTPEnabled,
			"sftp_subdomain":         cfg.SFTPSubdomain,
			"sftp_port":              cfg.SFTPPort,
			"tinyauth_enabled":       cfg.TinyAuthEnabled,
			"tinyauth_subdomain":     cfg.TinyAuthSubdomain,
			"tinyauth_port":          cfg.TinyAuthPort,
			"rustfs_enabled":         cfg.RustFSEnabled,
			"rustfs_subdomain":       cfg.RustFSSubdomain,
			"rustfs_port":            cfg.RustFSPort,
			"rustfs_console_port":    cfg.RustFSConsolePort,
			"rustfs_access_key":      cfg.RustFSAccessKey,
			"rustfs_secret_key":      cfg.RustFSSecretKey,
			"storage_size":           cfg.StorageSize,
			"version":                getVersion(),
		}
		json.NewEncoder(w).Encode(resp)
	})

	// GET /api/check-update — checks if an update is available
	mux.HandleFunc("/api/check-update", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		current := getVersion()
		latest, err := getLatestGcoreReleaseTag()
		if err != nil {
			log.Printf("[serve] Failed to check for latest update: %v", err)
			latest = current
		}

		resp := map[string]interface{}{
			"current_version": current,
			"latest_version":  latest,
			"update_available": current != latest && latest != "",
		}
		json.NewEncoder(w).Encode(resp)
	})

	// POST /api/update — performs self-update
	mux.HandleFunc("/api/update", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		latest, err := getLatestGcoreReleaseTag()
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Failed to find latest release tag: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		execPath, err := os.Executable()
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Failed to find executable path: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		arch := runtime.GOARCH
		url := fmt.Sprintf("https://github.com/The1462/gcore/releases/download/%s/gcore-linux-%s", latest, arch)
		log.Printf("[updater] Downloading update from %s...", url)

		tmpPath := execPath + ".tmp"
		_ = os.Remove(tmpPath)

		if err := downloadFile(url, tmpPath); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Download failed: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		if fi, err := os.Stat(tmpPath); err != nil || fi.Size() < 1000000 {
			_ = os.Remove(tmpPath)
			http.Error(w, `{"error":"Downloaded binary is invalid or incomplete"}`, http.StatusInternalServerError)
			return
		}

		// On Linux/Unix, replacing a running executable requires unlinking the old one first
		_ = os.Remove(execPath)
		if err := os.Rename(tmpPath, execPath); err != nil {
			_ = os.Remove(tmpPath)
			http.Error(w, fmt.Sprintf(`{"error":"Failed to replace binary: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		_ = os.Chmod(execPath, 0755)

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
		log.Println("[updater] Update successfully downloaded and installed. Restarting daemon service...")

		go func() {
			time.Sleep(1 * time.Second)
			triggerServiceRestart()
		}()
	})

	// POST /api/setup — saves configuration
	mux.HandleFunc("/api/setup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var payload struct {
			LDAPUserPass       string `json:"ldap_user_pass"`
			MasterUsername     string `json:"master_username"`
			OrgName            string `json:"org_name"`
			LDAPBaseDN         string `json:"ldap_base_dn"`
			LDAPPort           int    `json:"ldap_port"`
			HTTPPort           int    `json:"http_port"`
			CFDomain           string `json:"cf_domain"`
			CFApiToken         string `json:"cf_api_token"`
			ForgejoEnabled     bool   `json:"forgejo_enabled"`
			ForgejoSubdomain   string `json:"forgejo_subdomain"`
			ForgejoHTTPPort    int    `json:"forgejo_http_port"`
			ForgejoSSHPort     int    `json:"forgejo_ssh_port"`
			PocketIDEnabled    bool   `json:"pocket_id_enabled"`
			PocketIDSubdomain  string `json:"pocket_id_subdomain"`
			PocketIDPort       int    `json:"pocket_id_port"`
			StalwartEnabled    bool   `json:"stalwart_enabled"`
			StalwartSubdomain  string `json:"stalwart_subdomain"`
			StalwartHTTPPort   int    `json:"stalwart_http_port"`
			SFTPEnabled        bool   `json:"sftp_enabled"`
			SFTPSubdomain      string `json:"sftp_subdomain"`
			SFTPPort           int    `json:"sftp_port"`
			TinyAuthEnabled    bool   `json:"tinyauth_enabled"`
			TinyAuthSubdomain  string `json:"tinyauth_subdomain"`
			TinyAuthPort       int    `json:"tinyauth_port"`
			RustFSEnabled      bool   `json:"rustfs_enabled"`
			RustFSSubdomain    string `json:"rustfs_subdomain"`
			RustFSPort         int    `json:"rustfs_port"`
			RustFSConsolePort  int    `json:"rustfs_console_port"`
			RustFSAccessKey    string `json:"rustfs_access_key"`
			RustFSSecretKey    string `json:"rustfs_secret_key"`
			StorageSize        string `json:"storage_size"`
		}

		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Bad request: %s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		if payload.LDAPUserPass == "" {
			http.Error(w, `{"error":"LDAP admin password is required"}`, http.StatusBadRequest)
			return
		}

		var zoneID, accountID string
		if payload.CFDomain != "" && payload.CFApiToken != "" {
			api, err := cloudflare.NewWithAPIToken(payload.CFApiToken)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"Failed to initialize Cloudflare client: %s"}`, err.Error()), http.StatusBadRequest)
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			zones, err := api.ListZones(ctx, payload.CFDomain)
			cancel()
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"Cloudflare API error: %s"}`, err.Error()), http.StatusBadRequest)
				return
			}
			if len(zones) == 0 {
				http.Error(w, fmt.Sprintf(`{"error":"Domain %s not found in your Cloudflare account"}`, payload.CFDomain), http.StatusBadRequest)
				return
			}
			zoneID = zones[0].ID
			accountID = zones[0].Account.ID
		}

		cfgMutex.Lock()
		appConfig.LDAPUserPass = payload.LDAPUserPass
		appConfig.MasterUsername = payload.MasterUsername
		appConfig.OrgName = payload.OrgName
		appConfig.LDAPBaseDN = payload.LDAPBaseDN
		appConfig.LDAPPort = payload.LDAPPort
		appConfig.HTTPPort = payload.HTTPPort
		appConfig.CFDomain = payload.CFDomain
		appConfig.CFApiToken = payload.CFApiToken
		appConfig.CFZoneID = zoneID
		appConfig.CFAccountID = accountID

		appConfig.ForgejoEnabled = payload.ForgejoEnabled
		appConfig.ForgejoSubdomain = payload.ForgejoSubdomain
		appConfig.ForgejoHTTPPort = payload.ForgejoHTTPPort
		appConfig.ForgejoSSHPort = payload.ForgejoSSHPort

		appConfig.PocketIDEnabled = payload.PocketIDEnabled
		appConfig.PocketIDSubdomain = payload.PocketIDSubdomain
		appConfig.PocketIDPort = payload.PocketIDPort

		appConfig.StalwartEnabled = payload.StalwartEnabled
		appConfig.StalwartSubdomain = payload.StalwartSubdomain
		appConfig.StalwartHTTPPort = payload.StalwartHTTPPort

		appConfig.SFTPEnabled = payload.SFTPEnabled
		appConfig.SFTPSubdomain = payload.SFTPSubdomain
		appConfig.SFTPPort = payload.SFTPPort
		appConfig.TinyAuthEnabled = payload.TinyAuthEnabled
		appConfig.TinyAuthSubdomain = payload.TinyAuthSubdomain
		appConfig.TinyAuthPort = payload.TinyAuthPort

		appConfig.RustFSEnabled = payload.RustFSEnabled
		appConfig.RustFSSubdomain = payload.RustFSSubdomain
		appConfig.RustFSPort = payload.RustFSPort
		appConfig.RustFSConsolePort = payload.RustFSConsolePort
		appConfig.RustFSAccessKey = payload.RustFSAccessKey
		appConfig.RustFSSecretKey = payload.RustFSSecretKey
		appConfig.StorageSize = payload.StorageSize

		cfgMutex.Unlock()

		if err := SaveConfig(); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Failed to save: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
		log.Printf("[serve] Configuration saved by web UI")
	})

	// Start server
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	server := &http.Server{
		Addr:    addr,
		Handler: basicAuth(mux),
	}

	// Graceful shutdown channel
	stopSignal := make(chan struct{})
	go func() {
		if stopChan != nil {
			<-stopChan
		} else {
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
			<-quit
		}
		close(stopSignal)
	}()

	go func() {
		log.Printf("[serve] GCore web setup UI at http://%s", addr)
		if stopChan == nil {
			log.Printf("[serve] Press Ctrl+C to stop")
		}
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[serve] Server error: %v", err)
		}
	}()

	<-stopSignal
	log.Println("[serve] Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}

// basicAuth protects endpoints when the application has been configured.
func basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfgMutex.RLock()
		configured := appConfig.LDAPUserPass != ""
		username := appConfig.MasterUsername
		password := appConfig.LDAPUserPass
		cfgMutex.RUnlock()

		if !configured {
			next.ServeHTTP(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok || (user != "admin" && user != username) || pass != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="GCore Setup"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
