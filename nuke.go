package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudflare/cloudflare-go"
	"github.com/kardianos/service"
)

// RunNuke completely resets the system, tearing down local service daemons,
// host network interfaces, local configs, and Cloudflare configurations.
func RunNuke() {
	fmt.Print("\nWARNING: This will completely erase all GCore data, configurations, and Cloudflare tunnels.\nAre you sure you want to nuke the system? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(strings.ToLower(choice))
	if choice != "y" && choice != "yes" {
		log.Println("Nuke operation aborted.")
		return
	}

	log.Println("Initiating complete GCore system reset...")

	// 1. Read existing config for Cloudflare API clean-up
	if err := LoadConfig(); err != nil {
		log.Printf("Warning: Failed to load config file: %v", err)
	}

	cfgMutex.RLock()
	cfg := appConfig
	cfgMutex.RUnlock()

	// 2. Clean Cloudflare DNS and Tunnels
	if cfg.CFApiToken != "" && cfg.CFDomain != "" {
		log.Println("Accessing Cloudflare API to delete DNS and Tunnel configurations...")
		api, err := cloudflare.NewWithAPIToken(cfg.CFApiToken)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			zones, err := api.ListZones(ctx, cfg.CFDomain)
			if err != nil {
				log.Printf("Cloudflare API Error (ListZones): %v", err)
			} else if len(zones) > 0 {
				zone := zones[0]
				zoneRC := cloudflare.ZoneIdentifier(zone.ID)

				// Delete wildcard DNS CNAME record if present
				wildcardName := "*." + cfg.CFDomain
				records, _, err := api.ListDNSRecords(ctx, zoneRC, cloudflare.ListDNSRecordsParams{
					Name: wildcardName,
					Type: "CNAME",
				})
				if err != nil {
					log.Printf("Cloudflare API Error (ListDNSRecords): %v", err)
				} else {
					for _, rec := range records {
						log.Printf("[!] Deleting CNAME record: Name=%s, Content=%s", rec.Name, rec.Content)
						if delErr := api.DeleteDNSRecord(ctx, zoneRC, rec.ID); delErr != nil {
							log.Printf("Cloudflare API Error (DeleteDNSRecord): %v", delErr)
						}
					}
				}

				// Delete Cloudflare Tunnel
				accountRC := cloudflare.ResourceIdentifier(zone.Account.ID)
				tunnels, _, err := api.ListTunnels(ctx, accountRC, cloudflare.TunnelListParams{
					Name: "gcore-tunnel",
				})
				if err != nil {
					log.Printf("Cloudflare API Error (ListTunnels): %v", err)
				} else {
					for _, t := range tunnels {
						log.Printf("[!] Deleting Cloudflare Tunnel: Name=%s, ID=%s", t.Name, t.ID)
						if delErr := api.DeleteTunnel(ctx, accountRC, t.ID); delErr != nil {
							log.Printf("Cloudflare API Error (DeleteTunnel): %v", delErr)
						}
					}
				}
			} else {
				log.Printf("No Cloudflare zone found matching domain %s", cfg.CFDomain)
			}
			cancel()
		} else {
			log.Printf("[-] Failed to create Cloudflare API client: %v", err)
		}
	}

	// 3. Uninstall GCore Service
	prg := &program{}
	s, err := service.New(prg, getSvcConfig())
	if err == nil {
		log.Println("Stopping and uninstalling GCore background service...")
		_ = s.Stop()
		_ = s.Uninstall()
	}

	// 4. Clean host TAP network interface
	log.Println("Tearing down TAP network interface tap_gcore...")
	CleanupTapInterface("tap_gcore", "172.16.0.2")

	// 5. Clean /etc/gcore and VM artifacts
	log.Println("Wiping local configurations, databases, and logs...")
	_ = os.RemoveAll("/etc/gcore")

	basePath := ""
	isDev := false
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		if filepath.Base(execDir) == "bin" {
			basePath = filepath.Dir(execDir)
		} else {
			basePath = execDir
		}
		// Check if we are running in development/workspace environment
		if _, devErr := os.Stat(filepath.Join(basePath, "go.mod")); devErr == nil {
			isDev = true
		}
	}

	resolvePath := func(relativePath string) string {
		if filepath.IsAbs(relativePath) {
			return relativePath
		}
		if basePath != "" {
			return filepath.Join(basePath, relativePath)
		}
		return relativePath
	}

	_ = os.RemoveAll(resolvePath("vms/rootfs"))
	_ = os.Remove(resolvePath("vms/gcore_apps.img"))
	_ = os.Remove(resolvePath("vms/gcore_configs.img"))
	_ = os.Remove(resolvePath("vms/gcore_storage.img"))

	// 6. Delete the executable itself (only if NOT in dev workspace)
	if err == nil {
		if isDev {
			log.Println("[Development Mode] Skipping deletion of GCore executable.")
		} else {
			log.Printf("Deleting executable: %s", execPath)
			_ = os.Remove(execPath)
		}
	}

	log.Println("Nuke complete! Local system reset to pristine state.")
	os.Exit(0)
}
