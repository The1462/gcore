package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/kardianos/service"
)

type program struct {
	exit       chan struct{}
	vmStopChan chan struct{}
	wg         sync.WaitGroup
}

func (p *program) Start(s service.Service) error {
	p.exit = make(chan struct{})
	p.vmStopChan = make(chan struct{})
	p.wg.Add(1)
	go p.run()
	return nil
}

func (p *program) Stop(s service.Service) error {
	log.Println("Stopping GCore service daemon...")
	close(p.exit)
	close(p.vmStopChan)
	p.wg.Wait()
	log.Println("GCore service daemon stopped.")
	return nil
}

func (p *program) run() {
	defer p.wg.Done()

	log.Println("GCore service daemon started. Starting web setup UI on port 11462...")

	// 1. Start the HTTP setup server on port 11462 immediately
	go RunServe(11462, p.exit)

	// 2. Loop to monitor configuration and maintain the guest VM
	for {
		select {
		case <-p.exit:
			return
		default:
		}

		if err := LoadConfig(); err != nil {
			log.Printf("Service Warning: Failed to load config: %v", err)
		}

		if !isConfigured() {
			// Wait for configuration via web UI
			select {
			case <-p.exit:
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		// Ensure VM images and rootfs are ready automatically
		if err := ensureVMReady(); err != nil {
			log.Printf("Service Error: ensureVMReady failed: %v. Retrying in 10 seconds...", err)
			select {
			case <-p.exit:
				return
			case <-time.After(10 * time.Second):
				continue
			}
		}

		// Sync Cloudflare tunnel
		token, err := SetupCloudflareTunnel()
		if err != nil {
			log.Printf("Service Error: Cloudflare tunnel setup failed: %v. Retrying in 5 seconds...", err)
			select {
			case <-p.exit:
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		cfgMutex.RLock()
		cfgCopy := appConfig
		cfgMutex.RUnlock()

		execEnv := []string{
			"CLOUDFLARE_TOKEN=" + token,
			"PATH=/bin:/usr/bin:/sbin:/usr/sbin",
			"TERM=xterm",
		}

		guestCmd := generateGuestInitScript(cfgCopy, "sleep infinity")

		execArgs := []string{
			"/bin/bash",
			"-c",
			guestCmd,
		}

		vmCfg := VMRunnerConfig{
			Name:        "gcore-vm",
			VCPUs:       1,
			MemMB:       1536,
			RootFS:      "vms/rootfs",
			AppsDisk:    "vms/gcore_apps.img",
			ConfigsDisk: "vms/gcore_configs.img",
			StorageDisk: "vms/gcore_storage.img",
			ExecArgs:    execArgs,
			ExecEnv:     execEnv,
			TapDev:      "tap_gcore",
			Background:  true,
		}

		log.Println("Starting guest VM in background...")
		if err := StartVM(vmCfg, p.vmStopChan); err != nil {
			log.Printf("Service Warning: Guest VM exited with error: %v", err)
		} else {
			log.Println("Guest VM exited successfully.")
		}

		select {
		case <-p.exit:
			return
		case <-time.After(5 * time.Second):
			log.Println("Restarting Guest VM...")
		}
	}
}

// ensureVMReady checks if rootfs and disks exist. If not, it builds/formats them automatically.
func ensureVMReady() error {
	// Check if rootfs exists
	if _, err := os.Stat("vms/rootfs"); os.IsNotExist(err) {
		log.Println("RootFS not found. Automatically running prepareOS...")
		if err := prepareOS("vms/rootfs"); err != nil {
			return fmt.Errorf("prepareOS: %w", err)
		}
	}

	cfgMutex.RLock()
	cfg := appConfig
	cfgMutex.RUnlock()

	// Check if any of the three disk images are missing
	appsMissing := false
	if _, err := os.Stat("vms/gcore_apps.img"); os.IsNotExist(err) {
		appsMissing = true
	}
	configsMissing := false
	if _, err := os.Stat("vms/gcore_configs.img"); os.IsNotExist(err) {
		configsMissing = true
	}
	storageMissing := false
	if _, err := os.Stat("vms/gcore_storage.img"); os.IsNotExist(err) {
		storageMissing = true
	}

	if appsMissing || configsMissing {
		log.Println("Apps or Configs disk image missing. Automatically preparing disk images...")
		embedDir := "embed/linux_amd64"
		appsDir := "vms/apps_source"
		configsDir := "vms/configs_source"

		if err := copyServiceBinaries(embedDir, appsDir); err != nil {
			log.Printf("copyServiceBinaries Warning: %v", err)
		}

		if err := generateServiceConfigs(configsDir, cfg); err != nil {
			return fmt.Errorf("generateServiceConfigs: %w", err)
		}

		if err := prepareDisks(appsDir, "vms/gcore_apps.img", "768M",
			configsDir, "vms/gcore_configs.img", "128M"); err != nil {
			return fmt.Errorf("prepareDisks: %w", err)
		}
	}

	if storageMissing {
		log.Println("Storage disk image missing. Automatically formatting storage image...")
		size := cfg.StorageSize
		if size == "" {
			size = "1G"
		}
		if err := generateEmptyExt4Image("vms/gcore_storage.img", size); err != nil {
			return fmt.Errorf("generateEmptyExt4Image: %w", err)
		}
	}

	return nil
}

// getSvcConfig returns the kardianos service configuration
func getSvcConfig() *service.Config {
	svcConfig := &service.Config{
		Name:        "gcore",
		DisplayName: "GCore Daemon",
		Description: "This is the GCore background microVM daemon.",
		Arguments:   []string{"service", "run"},
	}

	if cwd, err := os.Getwd(); err == nil {
		svcConfig.WorkingDirectory = cwd
	}
	return svcConfig
}

// RunService handles the service installation, run and management actions
func RunService(action string) {
	prg := &program{}
	s, err := service.New(prg, getSvcConfig())
	if err != nil {
		log.Fatalf("Failed to initialize service library: %v", err)
	}

	if action == "run" {
		log.Println("Running GCore service daemon in foreground...")
		if err = s.Run(); err != nil {
			log.Fatal(err)
		}
		return
	}

	if action == "status" {
		cStatus := exec.Command("systemctl", "status", "gcore")
		cStatus.Stdout = os.Stdout
		cStatus.Stderr = os.Stderr
		_ = cStatus.Run()
		return
	}

	// Use standard service actions (install, uninstall, start, stop, restart)
	err = service.Control(s, action)
	if err != nil {
		log.Printf("Failed to perform service action %q: %v\n", action, err)
		log.Printf("Valid service actions: install, uninstall, start, stop, restart, status\n")
	} else {
		log.Printf("Successfully completed service action %q.\n", action)
	}
}

// AutoStartService installs and starts the service in interactive wizard flow
func AutoStartService() {
	prg := &program{}
	s, err := service.New(prg, getSvcConfig())
	if err != nil {
		log.Printf("Failed to initialize service for autostart: %v", err)
		return
	}

	log.Println("Updating GCore background service configuration...")
	// Stop and uninstall the service first to ensure a fresh, updated unit definition (e.g. correct path/cwd)
	_ = s.Stop()
	_ = s.Uninstall()

	err = s.Install()
	if err != nil {
		log.Printf("Failed to install service: %v\n(Make sure to run with sudo!)", err)
		return
	}
	log.Println("Service unit installed/updated successfully.")

	err = s.Start()
	if err != nil {
		log.Printf("Failed to start service: %v", err)
		return
	}
	log.Println("Service started successfully in background.")
}

// handleAutoServiceManagement checks if the service is installed. If not, it installs and starts it.
func handleAutoServiceManagement() {
	prg := &program{}
	s, err := service.New(prg, getSvcConfig())
	if err != nil {
		fmt.Printf("Error initializing service: %v\n", err)
		return
	}

	status, err := s.Status()
	if err != nil {
		// Service is not installed (or failed to query)
		fmt.Println("GCore background service is not installed. Installing...")
		err = s.Install()
		if err != nil {
			fmt.Printf("Failed to install service: %v\n(Please run with sudo/Administrator privileges!)\n", err)
			os.Exit(1)
		}
		fmt.Println("Service unit installed successfully.")

		err = s.Start()
		if err != nil {
			fmt.Printf("Failed to start service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service started successfully in background.")
		fmt.Println("Please access the setup page at http://localhost:11462 to complete configuration.")
		os.Exit(0)
	}

	// Service is installed. Let's make sure it is running.
	if status != service.StatusRunning {
		fmt.Println("GCore background service is installed but not running. Starting...")
		err = s.Start()
		if err != nil {
			fmt.Printf("Failed to start service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service started successfully in background.")
	} else {
		fmt.Println("GCore background service is already running.")
	}
	fmt.Println("Access the setup page at http://localhost:11462")
	os.Exit(0)
}

func isConfigured() bool {
	cfgMutex.RLock()
	defer cfgMutex.RUnlock()
	return appConfig.LDAPUserPass != ""
}
