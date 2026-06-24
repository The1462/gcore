package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RunConfig executes the config subcommand: show, get, or set
func RunConfig(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: gcore config <show|get|set> [key[=value] ...]")
		fmt.Println("  gcore config show              — dump all settings")
		fmt.Println("  gcore config get <key>          — get a single setting")
		fmt.Println("  gcore config set <key>=<value>  — set a setting and save")
		os.Exit(1)
	}

	sub := args[0]

	switch sub {
	case "show":
		cfgMutex.RLock()
		defer cfgMutex.RUnlock()

		fmt.Printf("Service Configuration:\n")
		fmt.Printf("  Directory Services:\n")
		fmt.Printf("    Org Name:         %s\n", appConfig.OrgName)
		fmt.Printf("    Master Username:  %s\n", appConfig.MasterUsername)
		fmt.Printf("    LDAP Base DN:     %s\n", appConfig.LDAPBaseDN)
		fmt.Printf("    LDAP Port:        %d\n", appConfig.LDAPPort)
		fmt.Printf("    HTTP Port:        %d\n", appConfig.HTTPPort)
		fmt.Printf("    LDAP Pass:        %s\n", mask(appConfig.LDAPUserPass))
		fmt.Printf("    JWT Secret:       %s\n", mask(appConfig.JWTSecret))

		fmt.Printf("  Cloudflare:\n")
		fmt.Printf("    Domain:           %s\n", appConfig.CFDomain)
		fmt.Printf("    API Token:        %s\n", mask(appConfig.CFApiToken))
		fmt.Printf("    Zone ID:          %s\n", appConfig.CFZoneID)
		fmt.Printf("    Account ID:       %s\n", appConfig.CFAccountID)
		fmt.Printf("    Tunnel ID:        %s\n", appConfig.CFTunnelID)
		fmt.Printf("    Tunnel Secret:    %s\n", mask(appConfig.CFTunnelSecret))

		fmt.Printf("  FRP Tunnel:\n")
		fmt.Printf("    Bind Port:        %d\n", appConfig.FRPBindPort)
		fmt.Printf("    Vhost HTTP Port:  %d\n", appConfig.FRPVhostHTTPPort)
		fmt.Printf("    Auth Token:       %s\n", mask(appConfig.FRPAuthToken))

		fmt.Printf("  Forgejo:\n")
		fmt.Printf("    Enabled:          %v\n", appConfig.ForgejoEnabled)
		fmt.Printf("    Subdomain:        %s\n", appConfig.ForgejoSubdomain)
		fmt.Printf("    HTTP Port:        %d\n", appConfig.ForgejoHTTPPort)
		fmt.Printf("    SSH Port:         %d\n", appConfig.ForgejoSSHPort)
		fmt.Printf("    OIDC Secret:      %s\n", mask(appConfig.ForgejoOIDCSecret))

		fmt.Printf("  Pocket ID:\n")
		fmt.Printf("    Enabled:          %v\n", appConfig.PocketIDEnabled)
		fmt.Printf("    Subdomain:        %s\n", appConfig.PocketIDSubdomain)
		fmt.Printf("    Port:             %d\n", appConfig.PocketIDPort)
		fmt.Printf("    Secret:           %s\n", mask(appConfig.PocketIDSecret))
		fmt.Printf("    Encryption Key:   %s\n", mask(appConfig.PocketIDEncryptionKey))

		fmt.Printf("  Stalwart (Mail):\n")
		fmt.Printf("    Enabled:          %v\n", appConfig.StalwartEnabled)
		fmt.Printf("    Subdomain:        %s\n", appConfig.StalwartSubdomain)
		fmt.Printf("    HTTP Port:        %d\n", appConfig.StalwartHTTPPort)
		fmt.Printf("    Admin Pass:       %s\n", mask(appConfig.StalwartAdminPassword))
		fmt.Printf("    OIDC Secret:      %s\n", mask(appConfig.StalwartOIDCSecret))
		fmt.Printf("    Ports:            SMTP=%d SMTPS=%d SUB=%d\n", appConfig.StalwartSMTPPort, appConfig.StalwartSMTPSPort, appConfig.StalwartSubmissionPort)
		fmt.Printf("                      IMAP=%d IMAPS=%d POP3=%d POP3S=%d Sieve=%d\n", appConfig.StalwartIMAPPort, appConfig.StalwartIMAPSPort, appConfig.StalwartPOP3Port, appConfig.StalwartPOP3SPort, appConfig.StalwartSievePort)

		fmt.Printf("  SFTP:\n")
		fmt.Printf("    Enabled:          %v\n", appConfig.SFTPEnabled)
		fmt.Printf("    Subdomain:        %s\n", appConfig.SFTPSubdomain)
		fmt.Printf("    Port:             %d\n", appConfig.SFTPPort)

		fmt.Printf("  TinyAuth:\n")
		fmt.Printf("    Enabled:          %v\n", appConfig.TinyAuthEnabled)
		fmt.Printf("    Subdomain:        %s\n", appConfig.TinyAuthSubdomain)
		fmt.Printf("    Port:             %d\n", appConfig.TinyAuthPort)

		fmt.Printf("  RustFS (S3):\n")
		fmt.Printf("    Enabled:          %v\n", appConfig.RustFSEnabled)
		fmt.Printf("    Subdomain:        %s\n", appConfig.RustFSSubdomain)
		fmt.Printf("    API Port:         %d\n", appConfig.RustFSPort)
		fmt.Printf("    Console Port:     %d\n", appConfig.RustFSConsolePort)
		fmt.Printf("    Access Key:       %s\n", appConfig.RustFSAccessKey)
		fmt.Printf("    Secret Key:       %s\n", mask(appConfig.RustFSSecretKey))
		fmt.Printf("    Storage Size:     %s\n", appConfig.StorageSize)

	case "get":
		if len(args) < 2 {
			fmt.Println("Usage: gcore config get <key>")
			os.Exit(1)
		}
		key := args[1]
		cfgMutex.RLock()
		v := getConfigField(key)
		cfgMutex.RUnlock()
		if v == "" {
			fmt.Printf("Unknown config key: %s\n", key)
			os.Exit(1)
		}
		fmt.Println(v)

	case "set":
		if len(args) < 2 {
			fmt.Println("Usage: gcore config set <key>=<value> ...")
			os.Exit(1)
		}
		cfgMutex.Lock()
		for _, arg := range args[1:] {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) != 2 {
				fmt.Printf("Invalid format: %s (expected key=value)\n", arg)
				os.Exit(1)
			}
			if err := setConfigField(parts[0], parts[1]); err != nil {
				fmt.Printf("Error setting %s: %v\n", parts[0], err)
				os.Exit(1)
			}
		}
		cfgMutex.Unlock()

		if err := SaveConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save config: %v\n", err)
		} else {
			fmt.Println("Configuration updated.")
		}

	default:
		fmt.Printf("Unknown config subcommand: %s\n", sub)
		os.Exit(1)
	}
}

// getConfigField returns the string value of a config field by its YAML tag.
func getConfigField(key string) string {
	switch key {
	case "cf_domain":
		return appConfig.CFDomain
	case "cf_api_token":
		return appConfig.CFApiToken
	case "cf_zone_id":
		return appConfig.CFZoneID
	case "cf_account_id":
		return appConfig.CFAccountID
	case "cf_tunnel_id":
		return appConfig.CFTunnelID
	case "cf_tunnel_secret":
		return appConfig.CFTunnelSecret
	case "ldap_user_pass":
		return appConfig.LDAPUserPass
	case "master_username":
		return appConfig.MasterUsername
	case "ldap_base_dn":
		return appConfig.LDAPBaseDN
	case "org_name":
		return appConfig.OrgName
	case "ldap_port":
		return fmt.Sprintf("%d", appConfig.LDAPPort)
	case "http_port":
		return fmt.Sprintf("%d", appConfig.HTTPPort)
	case "jwt_secret":
		return appConfig.JWTSecret
	case "frp_bind_port":
		return fmt.Sprintf("%d", appConfig.FRPBindPort)
	case "frp_vhost_http_port":
		return fmt.Sprintf("%d", appConfig.FRPVhostHTTPPort)
	case "frp_auth_token":
		return appConfig.FRPAuthToken
	case "forgejo_enabled":
		return fmt.Sprintf("%v", appConfig.ForgejoEnabled)
	case "forgejo_subdomain":
		return appConfig.ForgejoSubdomain
	case "forgejo_http_port":
		return fmt.Sprintf("%d", appConfig.ForgejoHTTPPort)
	case "forgejo_ssh_port":
		return fmt.Sprintf("%d", appConfig.ForgejoSSHPort)
	case "forgejo_oidc_secret":
		return appConfig.ForgejoOIDCSecret
	case "pocket_id_enabled":
		return fmt.Sprintf("%v", appConfig.PocketIDEnabled)
	case "pocket_id_subdomain":
		return appConfig.PocketIDSubdomain
	case "pocket_id_port":
		return fmt.Sprintf("%d", appConfig.PocketIDPort)
	case "pocket_id_secret":
		return appConfig.PocketIDSecret
	case "pocket_id_encryption_key":
		return appConfig.PocketIDEncryptionKey
	case "stalwart_enabled":
		return fmt.Sprintf("%v", appConfig.StalwartEnabled)
	case "stalwart_subdomain":
		return appConfig.StalwartSubdomain
	case "stalwart_http_port":
		return fmt.Sprintf("%d", appConfig.StalwartHTTPPort)
	case "stalwart_admin_password":
		return appConfig.StalwartAdminPassword
	case "stalwart_oidc_secret":
		return appConfig.StalwartOIDCSecret
	case "stalwart_smtp_port":
		return fmt.Sprintf("%d", appConfig.StalwartSMTPPort)
	case "stalwart_smtps_port":
		return fmt.Sprintf("%d", appConfig.StalwartSMTPSPort)
	case "stalwart_submission_port":
		return fmt.Sprintf("%d", appConfig.StalwartSubmissionPort)
	case "stalwart_imap_port":
		return fmt.Sprintf("%d", appConfig.StalwartIMAPPort)
	case "stalwart_imaps_port":
		return fmt.Sprintf("%d", appConfig.StalwartIMAPSPort)
	case "stalwart_pop3_port":
		return fmt.Sprintf("%d", appConfig.StalwartPOP3Port)
	case "stalwart_pop3s_port":
		return fmt.Sprintf("%d", appConfig.StalwartPOP3SPort)
	case "stalwart_sieve_port":
		return fmt.Sprintf("%d", appConfig.StalwartSievePort)
	case "sftp_enabled":
		return fmt.Sprintf("%v", appConfig.SFTPEnabled)
	case "sftp_subdomain":
		return appConfig.SFTPSubdomain
	case "sftp_port":
		return fmt.Sprintf("%d", appConfig.SFTPPort)
	case "tinyauth_enabled":
		return fmt.Sprintf("%v", appConfig.TinyAuthEnabled)
	case "tinyauth_subdomain":
		return appConfig.TinyAuthSubdomain
	case "tinyauth_port":
		return fmt.Sprintf("%d", appConfig.TinyAuthPort)
	case "rustfs_enabled":
		return fmt.Sprintf("%v", appConfig.RustFSEnabled)
	case "rustfs_subdomain":
		return appConfig.RustFSSubdomain
	case "rustfs_port":
		return fmt.Sprintf("%d", appConfig.RustFSPort)
	case "rustfs_console_port":
		return fmt.Sprintf("%d", appConfig.RustFSConsolePort)
	case "rustfs_access_key":
		return appConfig.RustFSAccessKey
	case "rustfs_secret_key":
		return appConfig.RustFSSecretKey
	case "storage_size":
		return appConfig.StorageSize
	default:
		return ""
	}
}

// setConfigField sets a config field by YAML tag name.
func setConfigField(key, value string) error {
	switch key {
	case "cf_domain":
		appConfig.CFDomain = value
	case "cf_api_token":
		appConfig.CFApiToken = value
	case "cf_zone_id":
		appConfig.CFZoneID = value
	case "cf_account_id":
		appConfig.CFAccountID = value
	case "cf_tunnel_id":
		appConfig.CFTunnelID = value
	case "cf_tunnel_secret":
		appConfig.CFTunnelSecret = value
	case "ldap_user_pass":
		appConfig.LDAPUserPass = value
	case "master_username":
		appConfig.MasterUsername = value
	case "ldap_base_dn":
		appConfig.LDAPBaseDN = value
	case "org_name":
		appConfig.OrgName = value
	case "ldap_port":
		return setInt(&appConfig.LDAPPort, value)
	case "http_port":
		return setInt(&appConfig.HTTPPort, value)
	case "jwt_secret":
		appConfig.JWTSecret = value
	case "frp_bind_port":
		return setInt(&appConfig.FRPBindPort, value)
	case "frp_vhost_http_port":
		return setInt(&appConfig.FRPVhostHTTPPort, value)
	case "frp_auth_token":
		appConfig.FRPAuthToken = value
	case "forgejo_enabled":
		return setBool(&appConfig.ForgejoEnabled, value)
	case "forgejo_subdomain":
		appConfig.ForgejoSubdomain = value
	case "forgejo_http_port":
		return setInt(&appConfig.ForgejoHTTPPort, value)
	case "forgejo_ssh_port":
		return setInt(&appConfig.ForgejoSSHPort, value)
	case "forgejo_oidc_secret":
		appConfig.ForgejoOIDCSecret = value
	case "pocket_id_enabled":
		return setBool(&appConfig.PocketIDEnabled, value)
	case "pocket_id_subdomain":
		appConfig.PocketIDSubdomain = value
	case "pocket_id_port":
		return setInt(&appConfig.PocketIDPort, value)
	case "pocket_id_secret":
		appConfig.PocketIDSecret = value
	case "pocket_id_encryption_key":
		appConfig.PocketIDEncryptionKey = value
	case "stalwart_enabled":
		return setBool(&appConfig.StalwartEnabled, value)
	case "stalwart_subdomain":
		appConfig.StalwartSubdomain = value
	case "stalwart_http_port":
		return setInt(&appConfig.StalwartHTTPPort, value)
	case "stalwart_admin_password":
		appConfig.StalwartAdminPassword = value
	case "stalwart_oidc_secret":
		appConfig.StalwartOIDCSecret = value
	case "stalwart_smtp_port":
		return setInt(&appConfig.StalwartSMTPPort, value)
	case "stalwart_smtps_port":
		return setInt(&appConfig.StalwartSMTPSPort, value)
	case "stalwart_submission_port":
		return setInt(&appConfig.StalwartSubmissionPort, value)
	case "stalwart_imap_port":
		return setInt(&appConfig.StalwartIMAPPort, value)
	case "stalwart_imaps_port":
		return setInt(&appConfig.StalwartIMAPSPort, value)
	case "stalwart_pop3_port":
		return setInt(&appConfig.StalwartPOP3Port, value)
	case "stalwart_pop3s_port":
		return setInt(&appConfig.StalwartPOP3SPort, value)
	case "stalwart_sieve_port":
		return setInt(&appConfig.StalwartSievePort, value)
	case "sftp_enabled":
		return setBool(&appConfig.SFTPEnabled, value)
	case "sftp_subdomain":
		appConfig.SFTPSubdomain = value
	case "sftp_port":
		return setInt(&appConfig.SFTPPort, value)
	case "tinyauth_enabled":
		return setBool(&appConfig.TinyAuthEnabled, value)
	case "tinyauth_subdomain":
		appConfig.TinyAuthSubdomain = value
	case "tinyauth_port":
		return setInt(&appConfig.TinyAuthPort, value)
	case "rustfs_enabled":
		return setBool(&appConfig.RustFSEnabled, value)
	case "rustfs_subdomain":
		appConfig.RustFSSubdomain = value
	case "rustfs_port":
		return setInt(&appConfig.RustFSPort, value)
	case "rustfs_console_port":
		return setInt(&appConfig.RustFSConsolePort, value)
	case "rustfs_access_key":
		appConfig.RustFSAccessKey = value
	case "rustfs_secret_key":
		appConfig.RustFSSecretKey = value
	case "storage_size":
		appConfig.StorageSize = value
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

func setInt(p *int, value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid integer: %s", value)
	}
	*p = n
	return nil
}

func setBool(p *bool, value string) error {
	b, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("invalid bool value: %s", value)
	}
	*p = b
	return nil
}

// RunStatus shows the current operational state of gcore.
func RunStatus() {
	fmt.Println("=== GCore Status ===")
	fmt.Println()

	// Config state
	cfgMutex.RLock()
	configured := appConfig.LDAPUserPass != ""
	hasCF := appConfig.CFDomain != ""
	cfgMutex.RUnlock()

	if configured {
		fmt.Println("Configuration:  CONFIGURED")
	} else {
		fmt.Println("Configuration:  NOT CONFIGURED  (run 'gcore setup')")
	}
	fmt.Printf("  Config file:  %s\n", configPath)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Println("  Config file:  (not found)")
	}

	fmt.Println()

	// Disk images
	checkDisk("Apps disk", "vms/gcore_apps.img")
	checkDisk("Configs disk", "vms/gcore_configs.img")
	checkDisk("Storage disk", "vms/gcore_storage.img")

	fmt.Println()

	// Rootfs
	fmt.Print("VM rootfs:      ")
	rootfsPath := "vms/rootfs"
	if fi, err := os.Stat(rootfsPath); err == nil && fi.IsDir() {
		count := countFiles(rootfsPath)
		fmt.Printf("READY (%d files)\n", count)
	} else {
		fmt.Println("NOT READY  (run 'make prepare-os')")
	}

	fmt.Println()

	// Cloudflare
	if hasCF {
		fmt.Println("Cloudflare:     CONFIGURED")
	} else {
		fmt.Println("Cloudflare:     (not configured)")
	}

	// Services
	fmt.Println()
	fmt.Println("Configured services:")
	cfgMutex.RLock()
	cfg := appConfig
	cfgMutex.RUnlock()

	checkService("LLDAP", cfg.LDAPUserPass != "")
	checkService("Caddy", true) // Always installed in rootfs
	checkService("Forgejo", cfg.ForgejoEnabled)
	checkService("Pocket ID", cfg.PocketIDEnabled)
	checkService("Stalwart", cfg.StalwartEnabled)
	checkService("SFTP", cfg.SFTPEnabled)
	checkService("TinyAuth", cfg.TinyAuthEnabled)
	checkService("RustFS", cfg.RustFSEnabled)

	fmt.Println()

	// Service binary checks
	fmt.Println("Service binaries (apps disk):")
	appsBinDir := "vms/apps_source/bin"
	expected := []string{"forgejo", "lldap", "pocket-id", "stalwart", "stalwart-cli", "tinyauth", "rustfs"}
	for _, b := range expected {
		path := filepath.Join(appsBinDir, b)
		if fi, err := os.Stat(path); err == nil {
			fmt.Printf("  %-15s %s (%d MB)\n", b, "✓", fi.Size()/1024/1024)
		} else {
			fmt.Printf("  %-15s ✗ (not found — run 'make prepare-services')\n", b)
		}
	}
}

func checkDisk(label, path string) {
	resolved := resolvePath(path)
	fmt.Printf("%-15s ", label+":")
	if fi, err := os.Stat(resolved); err == nil {
		sizeMB := fi.Size() / 1024 / 1024
		fmt.Printf("READY (%d MB)\n", sizeMB)
	} else {
		fmt.Println("NOT READY  (run 'make prepare-disks')")
	}
}

func checkService(name string, enabled bool) {
	status := "✗ disabled"
	if enabled {
		status = "✓ enabled"
	}
	fmt.Printf("  %-12s %s\n", name, status)
}

func countFiles(dir string) int {
	count := 0
	filepath.Walk(dir, func(_ string, fi os.FileInfo, _ error) error {
		if fi != nil && fi.Mode().IsRegular() {
			count++
		}
		return nil
	})
	return count
}

// RunLogs shows paths to service log files and instructions for viewing them.
func RunLogs() {
	fmt.Println("=== GCore Service Logs ===")
	fmt.Println()
	fmt.Println("Service logs run INSIDE the VM. After running 'gcore run-vm',")
	fmt.Println("logs are written to /var/log/ inside the VM filesystem.")
	fmt.Println()
	fmt.Println("To view logs, attach to the running VM or mount the rootfs:")
	fmt.Println()
	fmt.Println("  # From inside the VM (if you have shell access):")
	fmt.Println("    tail -f /var/log/caddy.log")
	fmt.Println("    tail -f /var/log/lldap.log")
	fmt.Println("    tail -f /var/log/forgejo.log")
	fmt.Println("    tail -f /var/log/pocket-id.log")
	fmt.Println("    tail -f /var/log/stalwart.log")
	fmt.Println("    tail -f /var/log/cloudflared.log")
	fmt.Println()
	fmt.Println("  # Build logs (on the host):")
	fmt.Println("    make prepare-os       # Docker build output")
	fmt.Println("    make prepare-disks    # mkfs.ext4 output")
	fmt.Println()
	fmt.Println("  # Sentinel files (bootstrap state):")
	fmt.Println("    /data/.bootstrap_done    # present after bootstrap completes")
	fmt.Println("    vms/configs_source/bootstrap.sh  # the bootstrap script itself")
}

// resolvePath resolves a relative path against the project root or binary location.
func resolvePath(relative string) string {
	if filepath.IsAbs(relative) {
		return relative
	}
	execPath, err := os.Executable()
	if err != nil {
		return relative
	}
	execDir := filepath.Dir(execPath)
	if filepath.Base(execDir) == "bin" {
		return filepath.Join(filepath.Dir(execDir), relative)
	}
	return filepath.Join(execDir, relative)
}

// mask obscures all but the first 4 and last 4 chars of sensitive values.
func mask(s string) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) <= 8 {
		return strings.Repeat("*", len(s))
	}
	return s[:4] + "..." + s[len(s)-4:]
}

