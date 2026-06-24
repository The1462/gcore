package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/cloudflare/cloudflare-go"
	"golang.org/x/term"
)

// RunSetup executes the interactive LDAP and Cloudflare configuration setup wizard.
func RunSetup() {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n==================================================")
	fmt.Println("         GCORE SYSTEM SETUP WIZARD")
	fmt.Println("==================================================")

	fmt.Println("\n--- Directory Services Configuration ---")

	// 1. Prompt for LDAP password securely with verification
	var password string
	for {
		password = promptPassword("Enter LDAP Directory Admin Password (min 6 chars)")
		confirm := promptPassword("Confirm LDAP Directory Admin Password")
		if password == confirm {
			break
		}
		fmt.Println("Error: Passwords do not match. Please try again.")
	}

	// 2. Prompt for Organization Name
	orgName := promptString(reader, "Enter Organization Name", "oggree", true)
	defaultBaseDN := fmt.Sprintf("dc=%s,dc=com", strings.ToLower(strings.ReplaceAll(orgName, " ", "")))

	// 3. Prompt for Master Username
	masterUsername := promptString(reader, "Enter Master Username", "sysadmin", true)

	// 4. Prompt for LDAP Base DN
	baseDN := promptString(reader, "Enter LDAP Base DN", defaultBaseDN, true)

	// 5. Prompt for LDAP Port
	ldapPort := promptInt(reader, "Enter LDAP Port", 3890)

	// 6. Prompt for HTTP Port
	httpPort := promptInt(reader, "Enter LDAP Web Admin UI Port", 17170)

	fmt.Println("\n--- Cloudflare Zero-Trust Configuration (Optional) ---")
	fmt.Println("Note: Skip these by hitting enter if you wish to run completely local/without public DNS.")

	var cfDomain, cfToken, zoneID, accountID string
	for {
		cfDomain = promptString(reader, "Enter Cloudflare Domain (e.g. oggree.com)", "", false)
		cfToken = promptString(reader, "Enter Cloudflare API Token (Zone:DNS:Edit, Zone:Read)", "", false)

		if (cfDomain == "" && cfToken != "") || (cfDomain != "" && cfToken == "") {
			fmt.Println("Warning: Both Domain and API Token must be provided (or both skipped).")
			continue
		}

		if cfDomain != "" && cfToken != "" {
			fmt.Println("\nVerifying Cloudflare Token and Domain permissions...")

			// Setup Cloudflare client
			api, err := cloudflare.NewWithAPIToken(cfToken)
			if err != nil {
				fmt.Printf("Error: Failed to initialize Cloudflare client: %v. Please try again.\n", err)
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			zones, err := api.ListZones(ctx, cfDomain)
			cancel()

			if err != nil || len(zones) == 0 {
				log.Printf("Cloudflare zone verification failed: %v", err)
				fmt.Println("Error: Could not verify domain in Cloudflare. Make sure:")
				fmt.Println("  1. The domain matches your Cloudflare account.")
				fmt.Println("  2. Your API token has permissions: Zone:DNS:Edit and Zone:Read.")
				fmt.Println("Let's try entering the Cloudflare details again.")
				continue
			}

			zoneID = zones[0].ID
			accountID = zones[0].Account.ID
			fmt.Println("✓ Cloudflare configuration verified successfully!")
		}
		break
	}

	cfgMutex.Lock()
	appConfig.LDAPUserPass = password
	appConfig.MasterUsername = masterUsername
	appConfig.LDAPBaseDN = baseDN
	appConfig.OrgName = orgName
	appConfig.LDAPPort = ldapPort
	appConfig.HTTPPort = httpPort

	appConfig.CFDomain = cfDomain
	appConfig.CFApiToken = cfToken
	appConfig.CFZoneID = zoneID
	appConfig.CFAccountID = accountID
	cfgMutex.Unlock()

	fmt.Println("\nSaving GCore settings...")
	if err := SaveConfig(); err != nil {
		log.Fatalf("Error: Failed to save config: %v", err)
	}

	fmt.Println("✓ Configuration initialized and saved successfully!")
	AutoStartService()
}

func promptPassword(promptText string) string {
	for {
		fmt.Printf("%s: ", promptText)
		bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println() // Print newline after hidden input
		if err != nil {
			// Fallback if terminal is not interactive
			reader := bufio.NewReader(os.Stdin)
			input, err := reader.ReadString('\n')
			if err != nil {
				log.Fatalf("Error reading input: %v", err)
			}
			input = strings.TrimSpace(input)
			if len(input) < 6 {
				fmt.Println("Error: Password must be at least 6 characters.")
				continue
			}
			return input
		}

		password := strings.TrimSpace(string(bytePassword))
		if len(password) < 6 {
			fmt.Println("Error: Password must be at least 6 characters.")
			continue
		}
		return password
	}
}

func promptInt(reader *bufio.Reader, promptText string, defaultValue int) int {
	for {
		fmt.Printf("%s [%d]: ", promptText, defaultValue)
		input, err := reader.ReadString('\n')
		if err != nil {
			log.Fatalf("Error reading input: %v", err)
		}
		input = strings.TrimSpace(input)

		if input == "" {
			return defaultValue
		}

		var val int
		_, err = fmt.Sscan(input, &val)
		if err != nil {
			fmt.Println("Error: Please enter a valid integer.")
			continue
		}
		return val
	}
}

func promptString(reader *bufio.Reader, promptText, defaultValue string, mandatory bool) string {
	for {
		if defaultValue != "" {
			fmt.Printf("%s [%s]: ", promptText, defaultValue)
		} else {
			fmt.Printf("%s: ", promptText)
		}

		input, err := reader.ReadString('\n')
		if err != nil {
			log.Fatalf("Error reading input: %v", err)
		}
		input = strings.TrimSpace(input)

		if input == "" {
			if defaultValue != "" {
				return defaultValue
			}
			if mandatory {
				fmt.Println("Error: This field is required.")
				continue
			}
		}
		return input
	}
}
