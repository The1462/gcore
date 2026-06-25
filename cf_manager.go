package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"

	"github.com/cloudflare/cloudflare-go"
)

// SetupCloudflareTunnel initializes, registers and syncs the Cloudflare Tunnel
// and updates the wildcard CNAME DNS record.
func SetupCloudflareTunnel() (string, error) {
	cfgMutex.RLock()
	cfg := appConfig
	cfgMutex.RUnlock()

	if cfg.CFApiToken == "" || cfg.CFAccountID == "" || cfg.CFZoneID == "" || cfg.CFDomain == "" {
		return "", fmt.Errorf("cloudflare API credentials or IDs are missing in configuration")
	}

	api, err := cloudflare.NewWithAPIToken(cfg.CFApiToken)
	if err != nil {
		return "", fmt.Errorf("failed to initialize Cloudflare API: %v", err)
	}

	ctx := context.Background()
	tunnelName := "gcore-tunnel"

	var tunnelID string
	var tunnelSecret string

	// Generate secret if not present
	if cfg.CFTunnelSecret == "" {
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(secretBytes); err != nil {
			return "", err
		}
		tunnelSecret = base64.StdEncoding.EncodeToString(secretBytes)

		cfgMutex.Lock()
		appConfig.CFTunnelSecret = tunnelSecret
		cfgMutex.Unlock()
		if err := SaveConfig(); err != nil {
			return "", fmt.Errorf("failed to save generated tunnel secret: %w", err)
		}
		log.Println("Generated new Tunnel Secret and saved to config.")
	} else {
		tunnelSecret = cfg.CFTunnelSecret
	}

	rc := cloudflare.ResourceIdentifier(cfg.CFAccountID)

	// 1. Check if tunnel exists
	tunnels, _, err := api.ListTunnels(ctx, rc, cloudflare.TunnelListParams{
		Name: tunnelName,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list tunnels: %v", err)
	}

	// If the tunnel ID in the configuration is empty, it means this is a fresh setup.
	// Any existing tunnel on Cloudflare with the same name will have an invalid secret.
	// We must delete it to recreate it with our new secret.
	if cfg.CFTunnelID == "" && len(tunnels) > 0 {
		for _, t := range tunnels {
			log.Printf("Config was wiped, but found existing tunnel %s (%s). Deleting to recreate with new secret...", t.Name, t.ID)
			err = api.DeleteTunnel(ctx, rc, t.ID)
			if err != nil {
				log.Printf("Warning: failed to delete existing tunnel %s: %v", t.ID, err)
			}
		}
		// Refresh list
		tunnels, _, err = api.ListTunnels(ctx, rc, cloudflare.TunnelListParams{
			Name: tunnelName,
		})
		if err != nil {
			return "", fmt.Errorf("failed to list tunnels after deletion: %v", err)
		}
	}

	if len(tunnels) == 0 {
		log.Println("Tunnel does not exist. Creating...")

		tunnel, err := api.CreateTunnel(ctx, rc, cloudflare.TunnelCreateParams{
			Name:   tunnelName,
			Secret: tunnelSecret,
		})
		if err != nil {
			return "", fmt.Errorf("failed to create tunnel: %v", err)
		}
		tunnelID = tunnel.ID

		cfgMutex.Lock()
		appConfig.CFTunnelID = tunnelID
		cfgMutex.Unlock()
		if err := SaveConfig(); err != nil {
			return "", fmt.Errorf("failed to save configuration: %w", err)
		}
		log.Println("Created Cloudflare Tunnel:", tunnelID)
	} else {
		tunnelID = tunnels[0].ID
		if appConfig.CFTunnelID != tunnelID {
			cfgMutex.Lock()
			appConfig.CFTunnelID = tunnelID
			cfgMutex.Unlock()
			if err := SaveConfig(); err != nil {
				return "", fmt.Errorf("failed to save configuration: %w", err)
			}
		}
		log.Println("Found existing Cloudflare Tunnel:", tunnelID)
	}

	// 2. Configure Tunnel Ingress Rules
	// In a single VM setup, the tunnel daemon runs inside the VM and routes directly to localhost services.
	var ingressRules []cloudflare.UnvalidatedIngressRule

	// LDAP (LLDAP HTTP)
	if cfg.LDAPUserPass != "" {
		ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
			Hostname: fmt.Sprintf("ldap.%s", cfg.CFDomain),
			Service:  fmt.Sprintf("http://localhost:%d", cfg.HTTPPort),
		})
	}

	// TinyAuth
	if cfg.TinyAuthEnabled {
		ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
			Hostname: fmt.Sprintf("%s.%s", cfg.TinyAuthSubdomain, cfg.CFDomain),
			Service:  "http://localhost:80",
		})
	}

	// Forgejo
	if cfg.ForgejoEnabled {
		ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
			Hostname: fmt.Sprintf("%s.%s", cfg.ForgejoSubdomain, cfg.CFDomain),
			Service:  "http://localhost:80",
		})
	}

	// Pocket ID
	if cfg.PocketIDEnabled {
		ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
			Hostname: fmt.Sprintf("%s.%s", cfg.PocketIDSubdomain, cfg.CFDomain),
			Service:  "http://localhost:80",
		})
	}

	// Stalwart
	if cfg.StalwartEnabled {
		ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
			Hostname: fmt.Sprintf("%s.%s", cfg.StalwartSubdomain, cfg.CFDomain),
			Service:  "http://localhost:80",
		})
	}

	// RustFS
	if cfg.RustFSEnabled {
		ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
			Hostname: fmt.Sprintf("%s.%s", cfg.RustFSSubdomain, cfg.CFDomain),
			Service:  "http://localhost:80",
		})
		ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
			Hostname: fmt.Sprintf("console-%s.%s", cfg.RustFSSubdomain, cfg.CFDomain),
			Service:  "http://localhost:80",
		})
	}

	// SFTP
	if cfg.SFTPEnabled {
		ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
			Hostname: fmt.Sprintf("%s.%s", cfg.SFTPSubdomain, cfg.CFDomain),
			Service:  "http://localhost:80",
		})
	}

	// GCore Admin Web UI (always enabled / default)
	ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
		Hostname: fmt.Sprintf("gcore.%s", cfg.CFDomain),
		Service:  "http://127.0.0.1:11462",
	})

	// Wildcard fallback
	ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
		Hostname: fmt.Sprintf("*.%s", cfg.CFDomain),
		Service:  "http://localhost:80",
	})
	ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
		Hostname: fmt.Sprintf("*.prod.%s", cfg.CFDomain),
		Service:  "http://localhost:80",
	})
	ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
		Hostname: fmt.Sprintf("*.test.%s", cfg.CFDomain),
		Service:  "http://localhost:80",
	})
	ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
		Hostname: fmt.Sprintf("*.users.%s", cfg.CFDomain),
		Service:  "http://localhost:80",
	})

	// Catch-all fallback
	ingressRules = append(ingressRules, cloudflare.UnvalidatedIngressRule{
		Service: "http_status:404",
	})

	_, err = api.UpdateTunnelConfiguration(ctx, rc, cloudflare.TunnelConfigurationParams{
		TunnelID: tunnelID,
		Config: cloudflare.TunnelConfiguration{
			Ingress: ingressRules,
		},
	})
	if err != nil {
		log.Printf("Warning: Failed to update tunnel ingress config: %v", err)
	} else {
		log.Println("Tunnel ingress configured successfully.")
	}

	// 3. Create/Update Wildcard CNAME in DNS
	zoneRC := cloudflare.ZoneIdentifier(cfg.CFZoneID)

	recordName := fmt.Sprintf("*.%s", cfg.CFDomain)
	target := fmt.Sprintf("%s.cfargotunnel.com", tunnelID)

	records, _, err := api.ListDNSRecords(ctx, zoneRC, cloudflare.ListDNSRecordsParams{
		Name: recordName,
		Type: "CNAME",
	})
	if err != nil {
		return "", fmt.Errorf("failed to list DNS records: %v", err)
	}

	if len(records) == 0 {
		log.Println("DNS CNAME missing. Creating wildcard CNAME...")
		proxied := true
		_, err := api.CreateDNSRecord(ctx, zoneRC, cloudflare.CreateDNSRecordParams{
			Type:    "CNAME",
			Name:    recordName,
			Content: target,
			Proxied: &proxied,
		})
		if err != nil {
			log.Printf("Warning: failed to create DNS record: %v", err)
		} else {
			log.Println("Created wildcard DNS record.")
		}
	} else {
		if records[0].Content != target {
			log.Println("DNS CNAME mismatch. Updating...")
			proxied := true
			_, err := api.UpdateDNSRecord(ctx, zoneRC, cloudflare.UpdateDNSRecordParams{
				ID:      records[0].ID,
				Type:    "CNAME",
				Name:    recordName,
				Content: target,
				Proxied: &proxied,
			})
			if err != nil {
				log.Printf("Warning: failed to update DNS record: %v", err)
			} else {
				log.Println("Updated wildcard DNS record.")
			}
		} else {
			log.Println("DNS CNAME is correctly configured.")
		}
	}

	// 4. Construct Tunnel Token
	type TokenPayload struct {
		A string `json:"a"`
		T string `json:"t"`
		S string `json:"s"`
	}
	payload := TokenPayload{
		A: cfg.CFAccountID,
		T: tunnelID,
		S: tunnelSecret,
	}
	jsonPayload, _ := json.Marshal(payload)
	token := base64.StdEncoding.EncodeToString(jsonPayload)

	return token, nil
}
