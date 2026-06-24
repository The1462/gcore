package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Cloudflare
	CFDomain       string `yaml:"cf_domain"`
	CFApiToken     string `yaml:"cf_api_token"`
	CFZoneID       string `yaml:"cf_zone_id"`
	CFAccountID    string `yaml:"cf_account_id"`
	CFTunnelID     string `yaml:"cf_tunnel_id"`
	CFTunnelSecret string `yaml:"cf_tunnel_secret"`

	// LDAP / Directory
	LDAPUserPass   string `yaml:"ldap_user_pass"`
	MasterUsername string `yaml:"master_username"`
	LDAPBaseDN     string `yaml:"ldap_base_dn"`
	OrgName        string `yaml:"org_name"`
	LDAPPort       int    `yaml:"ldap_port"`
	HTTPPort       int    `yaml:"http_port"`
	JWTSecret      string `yaml:"jwt_secret"`

	// FRP tunnel relay
	FRPBindPort      int    `yaml:"frp_bind_port"`
	FRPVhostHTTPPort int    `yaml:"frp_vhost_http_port"`
	FRPAuthToken     string `yaml:"frp_auth_token"`

	// Forgejo (Git hosting)
	ForgejoEnabled    bool   `yaml:"forgejo_enabled"`
	ForgejoSubdomain  string `yaml:"forgejo_subdomain"`
	ForgejoHTTPPort   int    `yaml:"forgejo_http_port"`
	ForgejoSSHPort    int    `yaml:"forgejo_ssh_port"`
	ForgejoOIDCSecret string `yaml:"forgejo_oidc_secret"`

	// Pocket ID (OIDC auth portal)
	PocketIDEnabled       bool   `yaml:"pocket_id_enabled"`
	PocketIDSubdomain     string `yaml:"pocket_id_subdomain"`
	PocketIDPort          int    `yaml:"pocket_id_port"`
	PocketIDSecret        string `yaml:"pocket_id_secret"`
	PocketIDEncryptionKey string `yaml:"pocket_id_encryption_key"`

	// Stalwart (Email)
	StalwartEnabled       bool   `yaml:"stalwart_enabled"`
	StalwartSubdomain     string `yaml:"stalwart_subdomain"`
	StalwartHTTPPort      int    `yaml:"stalwart_http_port"`
	StalwartAdminPassword string `yaml:"stalwart_admin_password"`
	StalwartOIDCSecret    string `yaml:"stalwart_oidc_secret"`
	StalwartSMTPPort      int    `yaml:"stalwart_smtp_port"`
	StalwartSMTPSPort     int    `yaml:"stalwart_smtps_port"`
	StalwartSubmissionPort int   `yaml:"stalwart_submission_port"`
	StalwartIMAPPort      int    `yaml:"stalwart_imap_port"`
	StalwartIMAPSPort     int    `yaml:"stalwart_imaps_port"`
	StalwartPOP3Port      int    `yaml:"stalwart_pop3_port"`
	StalwartPOP3SPort     int    `yaml:"stalwart_pop3s_port"`
	StalwartSievePort     int    `yaml:"stalwart_sieve_port"`

	// SFTP
	SFTPEnabled   bool   `yaml:"sftp_enabled"`
	SFTPSubdomain string `yaml:"sftp_subdomain"`
	SFTPPort      int    `yaml:"sftp_port"`

	// TinyAuth
	TinyAuthEnabled   bool   `yaml:"tinyauth_enabled"`
	TinyAuthSubdomain string `yaml:"tinyauth_subdomain"`
	TinyAuthPort      int    `yaml:"tinyauth_port"`

	// RustFS (S3 storage)
	RustFSEnabled     bool   `yaml:"rustfs_enabled"`
	RustFSSubdomain   string `yaml:"rustfs_subdomain"`
	RustFSPort        int    `yaml:"rustfs_port"`
	RustFSConsolePort int    `yaml:"rustfs_console_port"`
	RustFSAccessKey   string `yaml:"rustfs_access_key"`
	RustFSSecretKey   string `yaml:"rustfs_secret_key"`
	StorageSize       string `yaml:"storage_size"`
}

var (
	configPath = "config.yaml"
	cfgMutex   sync.RWMutex
	appConfig  Config
)

func init() {
	if envPath := os.Getenv("GCORE_CONFIG_PATH"); envPath != "" {
		configPath = envPath
	}
}

// LoadConfig reads the configuration file from config.yaml
func LoadConfig() error {
	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return nil // Return empty config
	} else if err != nil {
		return err
	}

	cfgMutex.Lock()
	defer cfgMutex.Unlock()
	return yaml.Unmarshal(data, &appConfig)
}

// SaveConfig updates the configuration file, merging with any existing parameters
func SaveConfig() error {
	cfgMutex.RLock()
	cfg := appConfig
	cfgMutex.RUnlock()

	var rawMap map[string]interface{}
	data, err := os.ReadFile(configPath)
	if err == nil {
		_ = yaml.Unmarshal(data, &rawMap)
	}
	if rawMap == nil {
		rawMap = make(map[string]interface{})
	}

	if cfg.JWTSecret == "" {
		cfg.JWTSecret = generateRandomSecret(32)
	}

	// Initialize secrets and defaults
	if cfg.ForgejoSubdomain == "" {
		cfg.ForgejoSubdomain = "git"
	}
	if cfg.ForgejoHTTPPort == 0 {
		cfg.ForgejoHTTPPort = 14680
	}
	if cfg.ForgejoSSHPort == 0 {
		cfg.ForgejoSSHPort = 14622
	}
	if cfg.ForgejoOIDCSecret == "" {
		cfg.ForgejoOIDCSecret = generateRandomSecret(24)
	}

	if cfg.PocketIDSubdomain == "" {
		cfg.PocketIDSubdomain = "pass"
	}
	if cfg.PocketIDPort == 0 {
		cfg.PocketIDPort = 14640
	}
	if cfg.PocketIDSecret == "" {
		cfg.PocketIDSecret = generateRandomSecret(24)
	}
	if cfg.PocketIDEncryptionKey == "" {
		cfg.PocketIDEncryptionKey = generateRandomSecret(32)
	}

	if cfg.StalwartSubdomain == "" {
		cfg.StalwartSubdomain = "mail"
	}
	if cfg.StalwartHTTPPort == 0 {
		cfg.StalwartHTTPPort = 14660
	}
	if cfg.StalwartAdminPassword == "" {
		cfg.StalwartAdminPassword = generateRandomSecret(16)
	}
	if cfg.StalwartOIDCSecret == "" {
		cfg.StalwartOIDCSecret = generateRandomSecret(24)
	}
	if cfg.StalwartSMTPPort == 0 { cfg.StalwartSMTPPort = 14625 }
	if cfg.StalwartSMTPSPort == 0 { cfg.StalwartSMTPSPort = 14646 }
	if cfg.StalwartSubmissionPort == 0 { cfg.StalwartSubmissionPort = 14687 }
	if cfg.StalwartIMAPPort == 0 { cfg.StalwartIMAPPort = 14614 }
	if cfg.StalwartIMAPSPort == 0 { cfg.StalwartIMAPSPort = 14693 }
	if cfg.StalwartPOP3Port == 0 { cfg.StalwartPOP3Port = 14611 }
	if cfg.StalwartPOP3SPort == 0 { cfg.StalwartPOP3SPort = 14695 }
	if cfg.StalwartSievePort == 0 { cfg.StalwartSievePort = 14619 }

	if cfg.SFTPSubdomain == "" {
		cfg.SFTPSubdomain = "files"
	}
	if cfg.SFTPPort == 0 {
		cfg.SFTPPort = 14670
	}

	if _, ok := rawMap["tinyauth_enabled"]; !ok {
		cfg.TinyAuthEnabled = true
	}
	if cfg.TinyAuthSubdomain == "" {
		cfg.TinyAuthSubdomain = "auth"
	}
	if cfg.TinyAuthPort == 0 {
		cfg.TinyAuthPort = 14690
	}

	if _, ok := rawMap["rustfs_enabled"]; !ok {
		cfg.RustFSEnabled = true
	}
	if cfg.RustFSSubdomain == "" {
		cfg.RustFSSubdomain = "s3"
	}
	if cfg.RustFSPort == 0 {
		cfg.RustFSPort = 14600
	}
	if cfg.RustFSConsolePort == 0 {
		cfg.RustFSConsolePort = 14601
	}
	if cfg.RustFSAccessKey == "" {
		cfg.RustFSAccessKey = "rustfsadmin"
	}
	if cfg.RustFSSecretKey == "" {
		cfg.RustFSSecretKey = generateRandomSecret(24)
	}
	if cfg.StorageSize == "" {
		cfg.StorageSize = "1G"
	}

	cfgMutex.Lock()
	appConfig = cfg
	cfgMutex.Unlock()

	// Update/merge our fields
	rawMap["cf_domain"] = cfg.CFDomain
	rawMap["cf_api_token"] = cfg.CFApiToken
	rawMap["cf_zone_id"] = cfg.CFZoneID
	rawMap["cf_account_id"] = cfg.CFAccountID
	rawMap["cf_tunnel_id"] = cfg.CFTunnelID
	rawMap["cf_tunnel_secret"] = cfg.CFTunnelSecret

	rawMap["ldap_user_pass"] = cfg.LDAPUserPass
	rawMap["master_username"] = cfg.MasterUsername
	rawMap["ldap_base_dn"] = cfg.LDAPBaseDN
	rawMap["org_name"] = cfg.OrgName
	rawMap["ldap_port"] = cfg.LDAPPort
	rawMap["http_port"] = cfg.HTTPPort
	rawMap["jwt_secret"] = cfg.JWTSecret

	rawMap["frp_bind_port"] = cfg.FRPBindPort
	rawMap["frp_vhost_http_port"] = cfg.FRPVhostHTTPPort
	rawMap["frp_auth_token"] = cfg.FRPAuthToken

	rawMap["forgejo_enabled"] = cfg.ForgejoEnabled
	rawMap["forgejo_subdomain"] = cfg.ForgejoSubdomain
	rawMap["forgejo_http_port"] = cfg.ForgejoHTTPPort
	rawMap["forgejo_ssh_port"] = cfg.ForgejoSSHPort
	rawMap["forgejo_oidc_secret"] = cfg.ForgejoOIDCSecret

	rawMap["pocket_id_enabled"] = cfg.PocketIDEnabled
	rawMap["pocket_id_subdomain"] = cfg.PocketIDSubdomain
	rawMap["pocket_id_port"] = cfg.PocketIDPort
	rawMap["pocket_id_secret"] = cfg.PocketIDSecret
	rawMap["pocket_id_encryption_key"] = cfg.PocketIDEncryptionKey

	rawMap["stalwart_enabled"] = cfg.StalwartEnabled
	rawMap["stalwart_subdomain"] = cfg.StalwartSubdomain
	rawMap["stalwart_http_port"] = cfg.StalwartHTTPPort
	rawMap["stalwart_admin_password"] = cfg.StalwartAdminPassword
	rawMap["stalwart_oidc_secret"] = cfg.StalwartOIDCSecret
	rawMap["stalwart_smtp_port"] = cfg.StalwartSMTPPort
	rawMap["stalwart_smtps_port"] = cfg.StalwartSMTPSPort
	rawMap["stalwart_submission_port"] = cfg.StalwartSubmissionPort
	rawMap["stalwart_imap_port"] = cfg.StalwartIMAPPort
	rawMap["stalwart_imaps_port"] = cfg.StalwartIMAPSPort
	rawMap["stalwart_pop3_port"] = cfg.StalwartPOP3Port
	rawMap["stalwart_pop3s_port"] = cfg.StalwartPOP3SPort
	rawMap["stalwart_sieve_port"] = cfg.StalwartSievePort

	rawMap["sftp_enabled"] = cfg.SFTPEnabled
	rawMap["sftp_subdomain"] = cfg.SFTPSubdomain
	rawMap["sftp_port"] = cfg.SFTPPort

	rawMap["tinyauth_enabled"] = cfg.TinyAuthEnabled
	rawMap["tinyauth_subdomain"] = cfg.TinyAuthSubdomain
	rawMap["tinyauth_port"] = cfg.TinyAuthPort

	rawMap["rustfs_enabled"] = cfg.RustFSEnabled
	rawMap["rustfs_subdomain"] = cfg.RustFSSubdomain
	rawMap["rustfs_port"] = cfg.RustFSPort
	rawMap["rustfs_console_port"] = cfg.RustFSConsolePort
	rawMap["rustfs_access_key"] = cfg.RustFSAccessKey
	rawMap["rustfs_secret_key"] = cfg.RustFSSecretKey
	rawMap["storage_size"] = cfg.StorageSize

	newData, err := yaml.Marshal(rawMap)
	if err != nil {
		return err
	}

	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(configPath, newData, 0600)
}

// generateServiceConfigs writes config files for all enabled services into configsDir.
func generateServiceConfigs(configsDir string, cfg Config) error {
	if err := os.MkdirAll(configsDir, 0755); err != nil {
		return fmt.Errorf("create configs dir: %w", err)
	}

	// Bootstrap files (post-boot service configuration)
	if err := generateBootstrapFiles(configsDir, cfg); err != nil {
		return fmt.Errorf("bootstrap files: %w", err)
	}

	// VM init script (runs on boot — uses multi-line bash, no libkrun restrictions)
	if err := writeVMInitScript(configsDir, cfg); err != nil {
		return fmt.Errorf("vm init script: %w", err)
	}

	// Caddyfile — reverse proxy routing
	if err := writeCaddyConfig(configsDir, cfg); err != nil {
		return fmt.Errorf("caddy config: %w", err)
	}

	// TinyAuth
	if cfg.TinyAuthEnabled {
		if err := writeTinyAuthConfig(configsDir, cfg); err != nil {
			return fmt.Errorf("tinyauth config: %w", err)
		}
		privKeyPath := filepath.Join(configsDir, "tinyauth_oidc_key")
		pubKeyPath := filepath.Join(configsDir, "tinyauth_oidc_key.pub")
		if err := generateOIDCKeys(privKeyPath, pubKeyPath); err != nil {
			return fmt.Errorf("generate oidc keys: %w", err)
		}
	}

	// LLDAP config
	if cfg.LDAPUserPass != "" {
		if err := writeLLDAPConfigTo(configsDir, cfg); err != nil {
			return fmt.Errorf("lldap config: %w", err)
		}
	}

	// Forgejo
	if cfg.ForgejoEnabled {
		if err := writeForgejoConfig(configsDir, cfg); err != nil {
			return fmt.Errorf("forgejo config: %w", err)
		}
	}

	// Pocket ID
	if cfg.PocketIDEnabled {
		if err := writePocketIDEnv(configsDir, cfg); err != nil {
			return fmt.Errorf("pocketid env config: %w", err)
		}
	}

	// Stalwart
	if cfg.StalwartEnabled {
		if err := writeStalwartConfig(configsDir, cfg); err != nil {
			return fmt.Errorf("stalwart config: %w", err)
		}
	}

	// SFTP
	if cfg.SFTPEnabled {
		if err := writeSFTPConfig(configsDir, cfg); err != nil {
			return fmt.Errorf("sftp config: %w", err)
		}
	}

	return nil
}

// copyServiceBinaries copies pre-compiled service binaries from srcEmbedDir into appsDir.
// srcEmbedDir should point to gcore's embed/linux_amd64/.
func copyServiceBinaries(srcEmbedDir, appsDir string) error {
	binaries := []string{"forgejo", "lldap", "pocket-id", "stalwart", "stalwart-cli", "tinyauth", "rustfs"}
	binDir := filepath.Join(appsDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	for _, name := range binaries {
		src := filepath.Join(srcEmbedDir, name)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			fmt.Printf("  [skip] %s — not found at %s\n", name, src)
			continue
		}
		dst := filepath.Join(binDir, name)
		// Use cp to preserve executable bits, avoid re-copying if same
		cmd := exec.Command("cp", "-u", src, dst)
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("  [warn] copy %s: %v — %s\n", name, err, string(out))
		} else {
			fmt.Printf("  [ok] %s -> bin/%s\n", src, name)
		}
	}
	return nil
}

// generateGuestInitScript builds the bash one-liner that runs inside the VM on boot.
// Must be printable ASCII only — no newlines (libkrun rejects control chars in exec args).
// If finalCmd is empty, defaults to "exec /bin/bash". Pass "sleep infinity" for daemon mode.
func generateGuestInitScript(cfg Config, finalCmd ...string) string {
	// Mount disks first, then exec the vm_init.sh from the configs disk
	// Must be single-line — no newlines (libkrun rejects control chars in exec args)
	suffix := ""
	if len(finalCmd) > 0 && finalCmd[0] != "" {
		suffix = " " + finalCmd[0]
	}
	return fmt.Sprintf(`mkdir -p /apps /configs; mount -t ext4 -o ro /dev/vda /apps 2>/dev/null; mount -t ext4 -o ro /dev/vdb /configs 2>/dev/null; exec /bin/bash /configs/vm_init.sh%s`, suffix)
}

// writeVMInitScript generates the main VM init script (vm_init.sh) that runs on boot.
// This file is baked into the configs disk — no libkrun restrictions on content.
func writeVMInitScript(configsDir string, cfg Config) error {
	script := `#!/bin/bash
# GCore VM init script — runs once when the microVM boots
env > /var/log/env.log
mount -t proc proc /proc 2>/dev/null
mount -t sysfs sysfs /sys 2>/dev/null

# Fix /etc/hosts (Docker export may lack localhost entry)
echo "127.0.0.1 localhost localhost.localdomain public.pyzor.org" > /etc/hosts
echo "::1 localhost localhost.localdomain" >> /etc/hosts

# Bring up loopback interface
ip link set lo up

# Bring up networking
for dev in /sys/class/net/*; do
  iface=$(basename "$dev")
  if [ "$iface" != lo ] && [ "$iface" != dummy0 ]; then
    ip link set "$iface" up
    ip addr add 172.16.0.2/24 dev "$iface"
    ip route add default via 172.16.0.1
    break
  fi
done
echo nameserver 8.8.8.8 > /etc/resolv.conf
mkdir -p /apps /configs /data
mount -t ext4 -o ro /dev/vda /apps 2>/dev/null
mount -t ext4 -o ro /dev/vdb /configs 2>/dev/null
mount -t ext4 -o rw /dev/vdc /data 2>/dev/null

# Create git user/group if they don't exist
if ! getent passwd git >/dev/null; then
  groupadd -g 1000 git 2>/dev/null || groupadd git
  useradd -u 1000 -g git -m -s /bin/bash git 2>/dev/null || useradd -g git -m -s /bin/bash git
fi


# Start Caddy reverse proxy
if [ -f /configs/Caddyfile ]; then
  /usr/bin/caddy start --config /configs/Caddyfile 2>/dev/null
elif [ -f /etc/caddy/Caddyfile ]; then
  /usr/bin/caddy start --config /etc/caddy/Caddyfile 2>/dev/null
fi
`
	// LLDAP
	if cfg.LDAPUserPass != "" {
		script += `
# LLDAP Directory Service
mkdir -p /var/lib/lldap
/usr/bin/lldap run --config-file /configs/lldap_config.toml >/var/log/lldap.log 2>&1 &
`
	}

	// TinyAuth
	if cfg.TinyAuthEnabled {
		script += `
# TinyAuth SSO Service
if [ -f /usr/bin/tinyauth ] && [ -f /configs/tinyauth.env ]; then
  mkdir -p /data
  set -a
  source /configs/tinyauth.env
  set +a
  /usr/bin/tinyauth >/var/log/tinyauth.log 2>&1 &
fi
`
	}

	// Forgejo
	if cfg.ForgejoEnabled {
		script += `
# Forgejo Git Service
if [ -f /usr/bin/forgejo ]; then
  mkdir -p /data/forgejo
  chown -R git:git /data/forgejo
  su git -s /bin/sh -c "FORGEJO_WORK_DIR=/data/forgejo FORGEJO_CUSTOM=/configs/forgejo/custom /usr/bin/forgejo web --config /configs/forgejo.ini" >/var/log/forgejo.log 2>&1 &
fi
`
	}

	// Pocket ID
	if cfg.PocketIDEnabled {
		script += `
# Pocket ID Auth Portal
if [ -f /usr/bin/pocket-id ] && [ -f /configs/pocket-id.env ]; then
  mkdir -p /data/pocket-id
  if [ -f /data/pocket-id/pocket-id.db ]; then
    sqlite3 /data/pocket-id/pocket-id.db "DELETE FROM kv WHERE key='application_lock';" 2>/dev/null || true
  fi
  set -a
  source /configs/pocket-id.env
  set +a
  /usr/bin/pocket-id >/var/log/pocket-id.log 2>&1 &
fi
`
	}

	// Stalwart
	if cfg.StalwartEnabled {
		script += fmt.Sprintf(`
# Stalwart Mail Server
if [ -f /usr/bin/stalwart ]; then
  mkdir -p /data/stalwart
  export STALWART_RECOVERY_ADMIN="admin:%s"
  export STALWART_RECOVERY_MODE_PORT="%d"
  /usr/bin/stalwart --config /configs/stalwart/config.json >/var/log/stalwart.log 2>&1 &
fi
`, cfg.StalwartAdminPassword, cfg.StalwartHTTPPort)
	}

	// RustFS
	if cfg.RustFSEnabled {
		script += fmt.Sprintf(`
# RustFS S3 Server
if [ -f /usr/bin/rustfs ]; then
  mkdir -p /data/rustfs
  export RUSTFS_ADDRESS="0.0.0.0:%d"
  export RUSTFS_CONSOLE_ADDRESS="0.0.0.0:%d"
  export RUSTFS_ACCESS_KEY_ID="%s"
  export RUSTFS_SECRET_ACCESS_KEY="%s"
  /usr/bin/rustfs server /data/rustfs >/var/log/rustfs.log 2>&1 &
fi
`, cfg.RustFSPort, cfg.RustFSConsolePort, cfg.RustFSAccessKey, cfg.RustFSSecretKey)
	}

	// Bootstrap
	if cfg.StalwartEnabled || cfg.PocketIDEnabled || cfg.ForgejoEnabled {
		script += `
# Run service bootstrap once (sentinel file guards re-runs)
if [ -f /configs/bootstrap.sh ] && [ ! -f /data/.bootstrap_done ]; then
  echo "[init] Running service bootstrap..."
  sh /configs/bootstrap.sh && touch /data/.bootstrap_done
fi
`
	}

	// Cloudflare tunnel
	script += `
# Cloudflare Tunnel (if CLOUDFLARE_TOKEN env var is set)
if [[ $CLOUDFLARE_TOKEN ]]; then
  /usr/bin/cloudflared tunnel run --token $CLOUDFLARE_TOKEN >/var/log/cloudflared.log 2>&1 &
fi

# Keep the shell alive
if [ "$1" ]; then
  exec "$@"
else
  exec /bin/bash
fi
`

	path := filepath.Join(configsDir, "vm_init.sh")
	return os.WriteFile(path, []byte(script), 0755)
}

// writeLLDAPConfigTo writes the LLDAP toml config to a specified directory.
func writeLLDAPConfigTo(configsDir string, cfg Config) error {
	content := fmt.Sprintf(`# LLDAP Config generated by GCore

ldap_host = "0.0.0.0"
ldap_port = %d
http_host = "0.0.0.0"
http_port = %d
database_url = "sqlite:///var/lib/lldap/lldap.db?mode=rwc"
key_file = "/var/lib/lldap/server_key"
ldap_base_dn = "%s"
jwt_secret = "%s"
ldap_user_pass = "%s"
force_ldap_user_pass_reset = false
assets_path = "/opt/lldap/amd64-lldap/app"
verbose = false
`, cfg.LDAPPort, cfg.HTTPPort, cfg.LDAPBaseDN, cfg.JWTSecret, cfg.LDAPUserPass)

	path := filepath.Join(configsDir, "lldap_config.toml")
	return os.WriteFile(path, []byte(content), 0644)
}

// writeCaddyConfig writes the Caddyfile with reverse proxy routes for all enabled services.
func writeCaddyConfig(configsDir string, cfg Config) error {
	domain := cfg.CFDomain
	if domain == "" {
		domain = "localhost"
	}

	var routes string

	if cfg.LDAPUserPass != "" {
		routes += fmt.Sprintf(`http://ldap.%s {
    reverse_proxy localhost:%d
}
`, domain, cfg.HTTPPort)
	}

	if cfg.TinyAuthEnabled {
		routes += fmt.Sprintf(`http://%s.%s {
    header Access-Control-Allow-Origin *
    header Access-Control-Allow-Methods "GET, POST, OPTIONS, PUT, DELETE"
    header Access-Control-Allow-Headers "Content-Type, Authorization, Origin, Accept, X-Requested-With"
    
    @options {
        method OPTIONS
    }
    handle @options {
        respond "" 204
    }

    reverse_proxy localhost:%d
}
`, cfg.TinyAuthSubdomain, domain, cfg.TinyAuthPort)
	}

	if cfg.ForgejoEnabled {
		routes += fmt.Sprintf(`http://%s.%s {
    reverse_proxy localhost:%d
}
`, cfg.ForgejoSubdomain, domain, cfg.ForgejoHTTPPort)
	}

	if cfg.PocketIDEnabled {
		routes += fmt.Sprintf(`http://%s.%s {
    reverse_proxy localhost:%d
}
`, cfg.PocketIDSubdomain, domain, cfg.PocketIDPort)
	}

	if cfg.StalwartEnabled {
		routes += fmt.Sprintf(`http://%s.%s {
    reverse_proxy localhost:%d
}
`, cfg.StalwartSubdomain, domain, cfg.StalwartHTTPPort)
	}

	if cfg.RustFSEnabled {
		routes += fmt.Sprintf(`http://%s.%s {
    reverse_proxy localhost:%d
}
http://console-%s.%s {
    reverse_proxy localhost:%d
}
`, cfg.RustFSSubdomain, domain, cfg.RustFSPort, cfg.RustFSSubdomain, domain, cfg.RustFSConsolePort)
	}

	// Wildcard fallback
	routes += fmt.Sprintf(`http://*.%s {
    reverse_proxy localhost:80
}
`, domain)

	caddyfile := fmt.Sprintf(`# Auto-generated Caddyfile by gcore
{
    auto_https off
}

%s
`, routes)

	path := filepath.Join(configsDir, "Caddyfile")
	return os.WriteFile(path, []byte(caddyfile), 0644)
}

func generateStdBase64Secret(length int) string {
	secretBytes := make([]byte, length)
	if _, err := rand.Read(secretBytes); err == nil {
		return base64.RawURLEncoding.EncodeToString(secretBytes)
	}
	return "fallback-secret"
}

// writeForgejoConfig writes the Forgejo INI configuration.
func writeForgejoConfig(configsDir string, cfg Config) error {
	domain := cfg.CFDomain
	if domain == "" {
		domain = "localhost"
	}
	rootURL := fmt.Sprintf("http://localhost:%d/", cfg.ForgejoHTTPPort)
	if cfg.CFDomain != "" {
		rootURL = fmt.Sprintf("https://%s.%s/", cfg.ForgejoSubdomain, cfg.CFDomain)
	}

	secretKey := generateRandomSecret(64)
	internalToken := generateRandomSecret(64)
	oauth2JWTSecret := generateStdBase64Secret(32)
	lfsJWTSecret := generateStdBase64Secret(32)

	content := fmt.Sprintf(`APP_NAME = gcore Git Service
RUN_MODE = prod
WORK_PATH = /data/forgejo

[database]
DB_TYPE = sqlite3
PATH = /data/forgejo/forgejo.db
SQLITE_JOURNAL_MODE = WAL

[repository]
ROOT = /data/forgejo/repositories

[server]
HTTP_ADDR = 0.0.0.0
SSH_DOMAIN = %[1]s
DOMAIN = %[1]s
HTTP_PORT = %[3]d
ROOT_URL = %[4]s
DISABLE_SSH = false
SSH_PORT = %[5]d
SSH_LISTEN_PORT = 2222
START_SSH_SERVER = true
BUILTIN_SSH_SERVER_USER = git
LFS_START_SERVER = true
LFS_JWT_SECRET = %[6]s
JWT_SECRET = %[9]s

[lfs]
JWT_SECRET = %[6]s

[security]
INSTALL_LOCK = true
SECRET_KEY = %[7]s
INTERNAL_TOKEN = %[8]s
JWT_SECRET = %[9]s

[oauth2]
JWT_SECRET = %[9]s

[service]
DISABLE_REGISTRATION = false
REQUIRE_SIGNIN_VIEW = false
ALLOW_ONLY_EXTERNAL_REGISTRATION = true

[oauth2_client]
ENABLE_AUTO_REGISTRATION = true
ACCOUNT_LINKING = auto

[session]
PROVIDER = file

[log]
MODE = console
LEVEL = info
`, domain, domain, cfg.ForgejoHTTPPort, rootURL, cfg.ForgejoSSHPort, lfsJWTSecret, secretKey, internalToken, oauth2JWTSecret)

	path := filepath.Join(configsDir, "forgejo.ini")
	return os.WriteFile(path, []byte(content), 0644)
}

// writePocketIDConfig writes the Pocket ID YAML configuration.
// writePocketIDEnv writes the Pocket ID environment configuration.
func writePocketIDEnv(configsDir string, cfg Config) error {
	externalURL := fmt.Sprintf("http://localhost:%d", cfg.PocketIDPort)
	if cfg.CFDomain != "" {
		externalURL = fmt.Sprintf("https://%s.%s", cfg.PocketIDSubdomain, cfg.CFDomain)
	}

	ldapAddress := fmt.Sprintf("ldap://localhost:%d", cfg.LDAPPort)
	ldapBaseDN := cfg.LDAPBaseDN
	ldapBindDN := fmt.Sprintf("uid=admin,ou=people,%s", ldapBaseDN)

	var envLines []string
	envLines = append(envLines,
		fmt.Sprintf("APP_URL=%q", externalURL),
		fmt.Sprintf("PORT=%d", cfg.PocketIDPort),
		fmt.Sprintf("ENCRYPTION_KEY=%q", cfg.PocketIDEncryptionKey),
		"DB_CONNECTION_STRING=\"/data/pocket-id/pocket-id.db\"",
		"TRUST_PROXY=\"true\"",
		"LDAP_ENABLED=\"true\"",
		fmt.Sprintf("LDAP_URL=%q", ldapAddress),
		fmt.Sprintf("LDAP_BIND_DN=%q", ldapBindDN),
		fmt.Sprintf("LDAP_BIND_PASSWORD=%q", cfg.LDAPUserPass),
		fmt.Sprintf("LDAP_USER_SEARCH_BASE=%q", fmt.Sprintf("ou=people,%s", ldapBaseDN)),
	)

	content := strings.Join(envLines, "\n") + "\n"
	path := filepath.Join(configsDir, "pocket-id.env")
	return os.WriteFile(path, []byte(content), 0644)
}

// writeStalwartConfig writes the Stalwart mail server database JSON configuration.
func writeStalwartConfig(configsDir string, cfg Config) error {
	configDir := filepath.Join(configsDir, "stalwart")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	config := `{"@type":"Sqlite","path":"/data/stalwart/stalwart.db"}`

	path := filepath.Join(configDir, "config.json")
	return os.WriteFile(path, []byte(config), 0644)
}

// writeSFTPConfig writes the SFTP server configuration.
func writeSFTPConfig(configsDir string, cfg Config) error {
	content := fmt.Sprintf(`# SFTP server config — generated by gcore
Port %d
Subsystem sftp internal-sftp
ChrootDirectory /data/sftp
`, cfg.SFTPPort)

	path := filepath.Join(configsDir, "sftp_config")
	return os.WriteFile(path, []byte(content), 0644)
}

// writeTinyAuthConfig writes the tinyauth.env configuration file.
func writeTinyAuthConfig(configsDir string, cfg Config) error {
	domain := cfg.CFDomain

	// ldap details
	ldapAddress := fmt.Sprintf("ldap://localhost:%d", cfg.LDAPPort)
	ldapBaseDN := cfg.LDAPBaseDN
	ldapBindDN := fmt.Sprintf("uid=admin,ou=people,%s", ldapBaseDN)
	ldapBindPassword := cfg.LDAPUserPass

	appURL := ""
	if domain != "" {
		appURL = fmt.Sprintf("https://%s.%s", cfg.TinyAuthSubdomain, domain)
	} else {
		appURL = fmt.Sprintf("http://localhost:%d", cfg.TinyAuthPort)
	}

	var envLines []string
	envLines = append(envLines,
		fmt.Sprintf("TINYAUTH_APPURL=%q", appURL),
		"TINYAUTH_DATABASE_DRIVER=\"sqlite\"",
		"TINYAUTH_DATABASE_PATH=\"/data/tinyauth.db\"",
		fmt.Sprintf("TINYAUTH_SERVER_PORT=%d", cfg.TinyAuthPort),
		"TINYAUTH_SERVER_ADDRESS=\"0.0.0.0\"",
		fmt.Sprintf("TINYAUTH_LDAP_ADDRESS=%q", ldapAddress),
		fmt.Sprintf("TINYAUTH_LDAP_BINDDN=%q", ldapBindDN),
		fmt.Sprintf("TINYAUTH_LDAP_BINDPASSWORD=%q", ldapBindPassword),
		fmt.Sprintf("TINYAUTH_LDAP_BASEDN=%q", ldapBaseDN),
		"TINYAUTH_LDAP_INSECURE=\"true\"",
		fmt.Sprintf("TINYAUTH_LDAP_SEARCHFILTER=%q", "(uid=%s)"),
		"TINYAUTH_LABELPROVIDER=\"none\"",
	)

	ldapUIDomain := "ldap.localhost"
	if domain != "" {
		ldapUIDomain = fmt.Sprintf("ldap.%s", domain)
	}
	envLines = append(envLines,
		fmt.Sprintf("TINYAUTH_APPS_LLDAP_CONFIG_DOMAIN=%q", ldapUIDomain),
		"TINYAUTH_APPS_LLDAP_LDAP_GROUPS=\"admin,lldap_admin\"",
		"TINYAUTH_APPS_LLDAP_OAUTH_GROUPS=\"admin,lldap_admin\"",
	)

	if domain != "" {
		envLines = append(envLines,
			fmt.Sprintf("TINYAUTH_COOKIE_DOMAIN=%q", domain),
			"TINYAUTH_SECURE_COOKIE=\"true\"",
			"TINYAUTH_COOKIE_SECURE=\"true\"",
		)
	}

	if cfg.ForgejoEnabled || cfg.StalwartEnabled {
		envLines = append(envLines,
			"TINYAUTH_OIDC_PRIVATEKEYPATH=\"/configs/tinyauth_oidc_key\"",
			"TINYAUTH_OIDC_PUBLICKEYPATH=\"/configs/tinyauth_oidc_key.pub\"",
		)
	}

	if cfg.ForgejoEnabled {
		redirectURIs := fmt.Sprintf("http://localhost:%d/user/oauth2/TinyAuth/callback", cfg.ForgejoHTTPPort)
		if domain != "" {
			redirectURIs = fmt.Sprintf("https://%s.%s/user/oauth2/TinyAuth/callback", cfg.ForgejoSubdomain, domain)
		}
		envLines = append(envLines,
			"TINYAUTH_OIDC_CLIENTS_FORGEJO_CLIENTID=\"forgejo\"",
			fmt.Sprintf("TINYAUTH_OIDC_CLIENTS_FORGEJO_CLIENTSECRET=%q", cfg.ForgejoOIDCSecret),
			"TINYAUTH_OIDC_CLIENTS_FORGEJO_NAME=\"Forgejo\"",
			fmt.Sprintf("TINYAUTH_OIDC_CLIENTS_FORGEJO_TRUSTEDREDIRECTURIS=%q", redirectURIs),
		)
	}

	if cfg.StalwartEnabled {
		redirectURIs := "http://localhost:14660/admin/oauth/callback"
		if domain != "" {
			redirectURIs = fmt.Sprintf("https://%s.%s/admin/oauth/callback", cfg.StalwartSubdomain, domain)
		}
		envLines = append(envLines,
			"TINYAUTH_OIDC_CLIENTS_STALWART_CLIENTID=\"stalwart-webui\"",
			"TINYAUTH_OIDC_CLIENTS_STALWART_NAME=\"Stalwart\"",
			fmt.Sprintf("TINYAUTH_OIDC_CLIENTS_STALWART_TRUSTEDREDIRECTURIS=%q", redirectURIs),
		)
	}

	if cfg.PocketIDEnabled {
		redirectURL := fmt.Sprintf("http://auth.localhost:%d/api/oauth/callback/pocketid", cfg.HTTPPort)
		if domain != "" {
			redirectURL = fmt.Sprintf("https://%s.%s/api/oauth/callback/pocketid", cfg.TinyAuthSubdomain, domain)
		}

		authURL := "http://localhost:14640/authorize"
		tokenURL := "http://127.0.0.1:14640/api/oidc/token"
		userInfoURL := "http://127.0.0.1:14640/api/oidc/userinfo"

		if domain != "" {
			authURL = fmt.Sprintf("https://%s.%s/authorize", cfg.PocketIDSubdomain, domain)
		}

		envLines = append(envLines,
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_CLIENTID=\"tinyauth\"",
			fmt.Sprintf("TINYAUTH_OAUTH_PROVIDERS_POCKETID_CLIENTSECRET=%q", cfg.PocketIDSecret),
			fmt.Sprintf("TINYAUTH_OAUTH_PROVIDERS_POCKETID_AUTHURL=%q", authURL),
			fmt.Sprintf("TINYAUTH_OAUTH_PROVIDERS_POCKETID_TOKENURL=%q", tokenURL),
			fmt.Sprintf("TINYAUTH_OAUTH_PROVIDERS_POCKETID_USERINFOURL=%q", userInfoURL),
			fmt.Sprintf("TINYAUTH_OAUTH_PROVIDERS_POCKETID_REDIRECTURL=%q", redirectURL),
			fmt.Sprintf("TINYAUTH_OAUTH_PROVIDERS_POCKETID_SCOPES=%q", "openid email profile"),
		)
	}

	content := strings.Join(envLines, "\n") + "\n"
	path := filepath.Join(configsDir, "tinyauth.env")
	return os.WriteFile(path, []byte(content), 0644)
}

// generateOIDCKeys generates a PEM-encoded RSA 2048 key pair.
func generateOIDCKeys(privateKeyPath, publicKeyPath string) error {
	if _, err := os.Stat(privateKeyPath); err == nil {
		return nil
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	privBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}
	privFile, err := os.OpenFile(privateKeyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open private key file: %w", err)
	}
	defer privFile.Close()
	if err := pem.Encode(privFile, privBlock); err != nil {
		return fmt.Errorf("failed to encode private key PEM: %w", err)
	}

	pubASN1, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to marshal public key: %w", err)
	}
	pubBlock := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubASN1,
	}
	pubFile, err := os.OpenFile(publicKeyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open public key file: %w", err)
	}
	defer pubFile.Close()
	if err := pem.Encode(pubFile, pubBlock); err != nil {
		return fmt.Errorf("failed to encode public key PEM: %w", err)
	}

	return nil
}

func generateRandomSecret(length int) string {
	secretBytes := make([]byte, length)
	if _, err := rand.Read(secretBytes); err == nil {
		return base64.RawURLEncoding.EncodeToString(secretBytes)
	}
	return "fallback-secret"
}

