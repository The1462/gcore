package main

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed vms/roles.ndjson
var rolesDataFS embed.FS

// generateBootstrapFiles writes post-boot configuration scripts and ndjson plans
// into the configs directory so the VM can self-configure on first boot.
func generateBootstrapFiles(configsDir string, cfg Config) error {
	if err := os.MkdirAll(configsDir, 0755); err != nil {
		return fmt.Errorf("create configs dir: %w", err)
	}

	// Stalwart bootstrap plans (Stage 1 + Stage 2)
	if cfg.StalwartEnabled {
		if err := writeStalwartBootstrap(configsDir, cfg); err != nil {
			return fmt.Errorf("stalwart bootstrap: %w", err)
		}
	}

	// Pocket ID OIDC client registration script
	if cfg.PocketIDEnabled {
		if err := writePocketIDBootstrap(configsDir, cfg); err != nil {
			return fmt.Errorf("pocketid bootstrap: %w", err)
		}
	}

	// Forgejo custom template for TinyAuth SSO redirect
	if cfg.ForgejoEnabled {
		if err := writeForgejoBootstrap(configsDir, cfg); err != nil {
			return fmt.Errorf("forgejo bootstrap: %w", err)
		}
	}

	// Master bootstrap orchestration script
	if err := writeMasterBootstrap(configsDir, cfg); err != nil {
		return fmt.Errorf("master bootstrap: %w", err)
	}

	return nil
}

// writeStalwartBootstrap generates both Stage 1 (initial domain setup)
// and Stage 2 (LDAP/OIDC directory config, listeners, accounts) ndjson plans.
func writeStalwartBootstrap(configsDir string, cfg Config) error {
	bsDir := filepath.Join(configsDir, "stalwart")
	if err := os.MkdirAll(bsDir, 0755); err != nil {
		return err
	}

	// Copy roles.ndjson from host workspace
	rolesData, err := rolesDataFS.ReadFile("vms/roles.ndjson")
	if err != nil {
		// Fallback to local files
		rolesData, err = os.ReadFile("vms/roles.ndjson")
		if err != nil {
			rolesData, err = os.ReadFile("../vms/roles.ndjson")
		}
	}
	if err == nil {
		if err := os.WriteFile(filepath.Join(bsDir, "roles.ndjson"), rolesData, 0644); err != nil {
			return fmt.Errorf("write roles.ndjson: %w", err)
		}
	} else {
		return fmt.Errorf("read roles.ndjson: %w", err)
	}

	domainName := cfg.CFDomain
	if domainName == "" {
		domainName = "localhost"
	}
	hostname := fmt.Sprintf("mail.%s", domainName)

	// --- Stage 2: Core configuration plan ---
	authURL := fmt.Sprintf("http://localhost:%d", cfg.TinyAuthPort)
	if cfg.CFDomain != "" {
		authURL = fmt.Sprintf("https://%s.%s", cfg.TinyAuthSubdomain, cfg.CFDomain)
	}

	var planLines []string

	// Destroy default listeners to avoid primaryKeyViolation
	planLines = append(planLines, `{"@type":"destroy","object":"NetworkListener"}`)

	// LDAP Directory (LLDAP)
	planLines = append(planLines, fmt.Sprintf(
		`{"@type":"create","object":"Directory","value":{"lldap-dir":{"@type":"Ldap","description":"LLDAP Directory","url":"ldap://localhost:%d","baseDn":%q,"bindDn":%q,"bindSecret":{"@type":"Value","secret":%q},"bindAuthentication":true,"filterLogin":"(&(objectClass=inetOrgPerson)(|(uid=?)(mail=?)))","filterMailbox":"(&(objectClass=inetOrgPerson)(mail=?))","attrSecretChanged":{"pwdChangedTime":true}}}}`,
		cfg.LDAPPort, cfg.LDAPBaseDN,
		fmt.Sprintf("uid=admin,ou=people,%s", cfg.LDAPBaseDN),
		cfg.LDAPUserPass,
	))

	// OIDC Directory (TinyAuth)
	planLines = append(planLines, fmt.Sprintf(
		`{"@type":"create","object":"Directory","value":{"tinyauth-dir":{"@type":"Oidc","description":"TinyAuth OIDC","issuerUrl":%q,"claimUsername":"preferred_username"}}}`,
		authURL,
	))

	// Set primary auth to TinyAuth OIDC
	planLines = append(planLines, `{"@type":"update","object":"Authentication","value":{"directoryId":"#tinyauth-dir","defaultAdminRoleIds":{"e":true},"defaultUserRoleIds":{"b":true}}}`)

	// Create domain
	planLines = append(planLines, fmt.Sprintf(
		`{"@type":"create","object":"Domain","value":{"primary-dom":{"name":%q,"directoryId":"#tinyauth-dir"}}}`,
		domainName,
	))

	// System settings
	planLines = append(planLines, fmt.Sprintf(
		`{"@type":"update","object":"SystemSettings","value":{"defaultDomainId":"#primary-dom","defaultHostname":%q}}`,
		hostname,
	))

	// Admin account (synced from LLDAP)
	planLines = append(planLines, fmt.Sprintf(
		`{"@type":"create","object":"Account","value":{"admin-acct":{"@type":"User","name":"admin","domainId":"#primary-dom","roles":{"@type":"Admin"}}}}`,
	))

	// Master account
	if cfg.MasterUsername != "" {
		planLines = append(planLines, fmt.Sprintf(
			`{"@type":"create","object":"Account","value":{"master-acct":{"@type":"User","name":%q,"domainId":"#primary-dom","roles":{"@type":"Admin"}}}}`,
			cfg.MasterUsername,
		))
	}

	// Network listeners (all ports)
	planLines = append(planLines, fmt.Sprintf(
		`{"@type":"create","object":"NetworkListener","value":{`+
			`"http":{"name":"http","protocol":"http","bind":{"[::]:%d":true}},`+
			`"smtp":{"name":"smtp","protocol":"smtp","bind":{"[::]:%d":true}},`+
			`"smtps":{"name":"smtps","protocol":"smtp","bind":{"[::]:%d":true},"tlsImplicit":true,"useTls":true},`+
			`"submission":{"name":"submission","protocol":"smtp","bind":{"[::]:%d":true}},`+
			`"imap":{"name":"imap","protocol":"imap","bind":{"[::]:%d":true}},`+
			`"imaps":{"name":"imaps","protocol":"imap","bind":{"[::]:%d":true},"tlsImplicit":true,"useTls":true},`+
			`"pop3":{"name":"pop3","protocol":"pop3","bind":{"[::]:%d":true}},`+
			`"pop3s":{"name":"pop3s","protocol":"pop3","bind":{"[::]:%d":true},"tlsImplicit":true,"useTls":true},`+
			`"sieve":{"name":"sieve","protocol":"manageSieve","bind":{"[::]:%d":true}}`+
			`}}`,
		cfg.StalwartHTTPPort,
		cfg.StalwartSMTPPort,
		cfg.StalwartSMTPSPort,
		cfg.StalwartSubmissionPort,
		cfg.StalwartIMAPPort,
		cfg.StalwartIMAPSPort,
		cfg.StalwartPOP3Port,
		cfg.StalwartPOP3SPort,
		cfg.StalwartSievePort,
	))

	// WebUI Application
	planLines = append(planLines, `{"@type":"create","object":"Application","value":{"webui":{"description":"WebUI","enabled":true,"resourceUrl":"https://github.com/stalwartlabs/webui/releases/latest/download/webui.zip","urlPrefix":{"/admin":true,"/account":true}}}}`)

	// --- Stage 1: Bootstrap plan ---
	bootstrapPlan := fmt.Sprintf(
		`{"@type":"update","object":"Bootstrap","value":{"defaultDomain":%q,"serverHostname":%q,"requestTlsCertificate":false,"generateDkimKeys":false,"dataStore":{"@type":"Sqlite","path":"/data/stalwart/stalwart.db","poolMaxConnections":10}}}`+"\n",
		domainName,
		hostname,
	)
	if err := os.WriteFile(filepath.Join(bsDir, "bootstrap_plan.ndjson"), []byte(bootstrapPlan), 0644); err != nil {
		return err
	}

	stage2Plan := strings.Join(planLines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(bsDir, "core_config_plan.ndjson"), []byte(stage2Plan), 0644); err != nil {
		return err
	}

	return nil
}

// writePocketIDBootstrap generates a bash script that registers OIDC clients
// in Pocket ID's SQLite database after Pocket ID starts and runs its migrations.
func writePocketIDBootstrap(configsDir string, cfg Config) error {
	callbackURL := fmt.Sprintf("http://auth.localhost:%d/api/oauth/callback/pocketid", cfg.HTTPPort)
	if cfg.CFDomain != "" {
		callbackURL = fmt.Sprintf("https://auth.%s/api/oauth/callback/pocketid", cfg.CFDomain)
	}

	// Pocket ID's OIDC client registration uses bcrypt-hashed secrets stored in SQLite.
	// The hashed secret is baked into the script so Pocket ID can verify it.
	// Generate the bcrypt hash of the client secret using python3 on the host.
	cmd := exec.Command("python3", "-c", fmt.Sprintf(`import bcrypt; print(bcrypt.hashpw(b%q, bcrypt.gensalt(rounds=12)).decode())`, cfg.PocketIDSecret))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to generate bcrypt hash of pocket id secret: %w: %s", err, string(out))
	}
	hashedSecret := strings.TrimSpace(string(out))

	scriptContent := fmt.Sprintf(`#!/bin/sh
# Pocket ID OIDC client bootstrap — generated by gcore
# Runs after Pocket ID starts and creates its SQLite tables

DB=/data/pocket-id/pocket-id.db
WAIT_MAX=120

echo "[pocketid-bootstrap] Waiting for database..."
for i in $(seq 1 $WAIT_MAX); do
    if [ -f "$DB" ]; then
        if sqlite3 "$DB" "SELECT name FROM sqlite_master WHERE type='table' AND name='oidc_clients';" 2>/dev/null | grep -q "oidc_clients"; then
            break
        fi
    fi
    sleep 1
done

echo "[pocketid-bootstrap] Registering TinyAuth OIDC client..."
sqlite3 "$DB" <<'SQL'
INSERT INTO oidc_clients (
    id, created_at, name, secret, callback_urls, logout_callback_urls,
    is_public, pkce_enabled, requires_reauthentication,
    is_group_restricted, credentials
) VALUES (
    'tinyauth',
    datetime('now'),
    'TinyAuth',
    '%s',
    '["%s"]',
    '[]',
    0,
    0,
    0,
    0,
    '{"federatedIdentities":null}'
)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    secret = excluded.secret,
    callback_urls = excluded.callback_urls;
SQL

echo "[pocketid-bootstrap] Done."
`, hashedSecret, callbackURL)

	path := filepath.Join(configsDir, "pocket-id-bootstrap.sh")
	return os.WriteFile(path, []byte(scriptContent), 0755)
}

// writeForgejoBootstrap generates the custom template that auto-redirects
// to TinyAuth OIDC SSO for Forgejo.
func writeForgejoBootstrap(configsDir string, cfg Config) error {
	tmplDir := filepath.Join(configsDir, "forgejo", "custom", "templates", "custom")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		return err
	}

	tmplContent := `<script>
if (window.location.pathname === '/user/login' && !window.location.search.includes('local=1')) {
    window.location.href = "/user/oauth2/TinyAuth" + window.location.search;
}
</script>
`

	path := filepath.Join(tmplDir, "header.tmpl")
	return os.WriteFile(path, []byte(tmplContent), 0644)
}

// writeMasterBootstrap generates the top-level bootstrap.sh that orchestrates
// all service post-boot configuration inside the VM.
func writeMasterBootstrap(configsDir string, cfg Config) error {
	var cmds []string

	hasAppsDisk := false
	if _, err := os.Stat("vms/gcore_apps.img"); err == nil {
		hasAppsDisk = true
	}

	stalwartCliPath := "/usr/bin/stalwart-cli"
	stalwartPath := "/usr/bin/stalwart"
	forgejoPath := "/usr/bin/forgejo"

	if hasAppsDisk {
		stalwartCliPath = "/apps/bin/stalwart-cli"
		stalwartPath = "/apps/bin/stalwart"
		forgejoPath = "/apps/bin/forgejo"
	}

	cmds = append(cmds, `#!/bin/sh
# GCore Service Bootstrap — runs once after all services are healthy
# Generated by gcore

set -e

# Clear application locks on Pocket ID
if [ -f /data/pocket-id/pocket-id.db ]; then
    echo "[bootstrap] Clearing Pocket ID application locks..."
    sqlite3 /data/pocket-id/pocket-id.db "DELETE FROM kv WHERE key='application_lock';" 2>/dev/null || true
fi

# Function: wait for TCP port
wait_for_port() {
    local host=$1 port=$2 name=$3 timeout=${4:-120}
    echo "[bootstrap] Waiting for $name on $host:$port..."
    for i in $(seq 1 $timeout); do
        if nc -z "$host" "$port" 2>/dev/null || curl -s -I "http://$host:$port/" >/dev/null 2>&1; then
            echo "[bootstrap] $name is ready."
            return 0
        fi
        sleep 1
    done
    echo "[bootstrap] WARNING: $name not ready after ${timeout}s"
    return 1
}

# Wait for critical services
`)

	// Wait for LLDAP first (needed by auth)
	if cfg.LDAPUserPass != "" {
		cmds = append(cmds, fmt.Sprintf(
			`wait_for_port localhost %d "LLDAP" 60`, cfg.HTTPPort))
	}

	// Wait for Stalwart HTTP
	if cfg.StalwartEnabled {
		cmds = append(cmds, `wait_for_port localhost 8080 "Stalwart" 120`)
	}

	// Wait for Pocket ID
	if cfg.PocketIDEnabled {
		cmds = append(cmds, fmt.Sprintf(
			`wait_for_port localhost %d "Pocket ID" 120`, cfg.PocketIDPort))
	}

	// Wait for Forgejo
	if cfg.ForgejoEnabled {
		cmds = append(cmds, fmt.Sprintf(
			`wait_for_port localhost %d "Forgejo" 120`, cfg.ForgejoHTTPPort))
	}

	cmds = append(cmds, "")
	cmds = append(cmds, `echo "[bootstrap] All services are healthy. Running service-specific bootstraps..."`)
	cmds = append(cmds, "")

	// Stalwart bootstrap
	if cfg.StalwartEnabled {
		cmds = append(cmds, `echo "--- Stalwart Bootstrap ---"`)
		cmds = append(cmds, fmt.Sprintf(
			`STALWART_URL="http://localhost:8080" STALWART_USER="admin" STALWART_PASSWORD="%[1]s" `+stalwartCliPath+` apply --file /configs/stalwart/bootstrap_plan.ndjson 2>&1 || echo "[bootstrap] Stalwart bootstrap plan may have already been applied"`, cfg.StalwartAdminPassword))
		cmds = append(cmds, fmt.Sprintf(
			`STALWART_URL="http://localhost:8080" STALWART_USER="admin" STALWART_PASSWORD="%[1]s" `+stalwartCliPath+` apply --file /configs/stalwart/core_config_plan.ndjson 2>&1 || echo "[bootstrap] Stalwart core config plan may have already been applied"`, cfg.StalwartAdminPassword))
		cmds = append(cmds, `echo "[bootstrap] Restarting Stalwart in normal mode..."`)
		cmds = append(cmds, `cp /var/log/stalwart.log /var/log/stalwart_recovery.log || true`)
		cmds = append(cmds, `pkill -x stalwart || true`)
		cmds = append(cmds, `sleep 2`)
		cmds = append(cmds, `unset STALWART_RECOVERY_MODE STALWART_RECOVERY_ADMIN STALWART_RECOVERY_MODE_PORT`)
		cmds = append(cmds, stalwartPath+` --config /configs/stalwart/config.json >/var/log/stalwart.log 2>&1 &`)

	}

	// Pocket ID bootstrap
	if cfg.PocketIDEnabled {
		cmds = append(cmds, `echo "--- Pocket ID Bootstrap ---"`)
		cmds = append(cmds, `sh /configs/pocket-id-bootstrap.sh 2>&1 || echo "[bootstrap] Pocket ID bootstrap may have already been applied"`)
	}


	// Forgejo OAuth/SSO configuration in bootstrap.sh
	if cfg.ForgejoEnabled && cfg.TinyAuthEnabled {
		domain := cfg.CFDomain
		if domain == "" {
			domain = "localhost"
		}
		cmds = append(cmds, `echo "--- Forgejo Bootstrap (TinyAuth OIDC) ---"`)
		cmds = append(cmds, fmt.Sprintf(`
# Wait for Forgejo to start
for i in $(seq 1 60); do
    if nc -z localhost %[1]d 2>/dev/null; then
        break
    fi
    sleep 2
done

# Register TinyAuth if not already registered (run as git user, forgejo refuses root)
if ! su git -s /bin/sh -c "FORGEJO_WORK_DIR=/data/forgejo FORGEJO_CUSTOM=/configs/forgejo/custom `+forgejoPath+` --config /configs/forgejo.ini admin auth list" 2>/dev/null | grep -q "TinyAuth"; then
    echo "[bootstrap] Registering TinyAuth OIDC in Forgejo..."
    su git -s /bin/sh -c "FORGEJO_WORK_DIR=/data/forgejo FORGEJO_CUSTOM=/configs/forgejo/custom `+forgejoPath+` --config /configs/forgejo.ini admin auth add-oauth \
        --provider openidConnect \
        --name TinyAuth \
        --key forgejo \
        --secret %[2]s \
        --auto-discover-url http://localhost:%[3]d/.well-known/openid-configuration"
        
    # Explicitly create the Master Username so OIDC auto-linking succeeds
    su git -s /bin/sh -c "FORGEJO_WORK_DIR=/data/forgejo FORGEJO_CUSTOM=/configs/forgejo/custom `+forgejoPath+` --config /configs/forgejo.ini admin user create --admin --username %[4]s --password '%[5]s' --email %[4]s@%[6]s --must-change-password=false" || true
    
    # Restart forgejo to pick up the new auth source
    pkill -f "forgejo web" || true
    sleep 2
    su git -s /bin/sh -c "FORGEJO_WORK_DIR=/data/forgejo FORGEJO_CUSTOM=/configs/forgejo/custom `+forgejoPath+` --config /configs/forgejo.ini web" >/var/log/forgejo.log 2>&1 &
fi
`, cfg.ForgejoHTTPPort, cfg.ForgejoOIDCSecret, cfg.TinyAuthPort, cfg.MasterUsername, cfg.LDAPUserPass, domain))
	}

	cmds = append(cmds, "")
	cmds = append(cmds, `echo "[bootstrap] GCore service bootstrap complete."`)

	content := strings.Join(cmds, "\n")
	path := filepath.Join(configsDir, "bootstrap.sh")
	return os.WriteFile(path, []byte(content), 0755)
}
