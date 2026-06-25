package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

type VMRunnerConfig struct {
	Name        string   `json:"Name"`
	VCPUs       int      `json:"VCPUs"`
	MemMB       int      `json:"MemMB"`
	RootFS      string   `json:"RootFS"`
	AppsDisk    string   `json:"AppsDisk"`
	ConfigsDisk string   `json:"ConfigsDisk"`
	StorageDisk string   `json:"StorageDisk"`
	ExecArgs    []string `json:"ExecArgs"`
	ExecEnv     []string `json:"ExecEnv"`
	TapDev      string   `json:"TapDev"`
	Background  bool     `json:"-"`
}

// StartVM sets up networking, writes VM config, and launches the krun_worker process.
func StartVM(cfg VMRunnerConfig, stopChan <-chan struct{}) error {
	// Host IP network parameters
	hostIP := "172.16.0.1"
	guestIP := "172.16.0.2"

	// 1. Setup host TAP network interface
	if cfg.TapDev != "" {
		log.Printf("[%s] Setting up host TAP network interface %s...", cfg.Name, cfg.TapDev)
		if err := SetupTapInterface(cfg.TapDev, hostIP, guestIP); err != nil {
			return fmt.Errorf("failed to setup tap interface: %w", err)
		}
		defer func() {
			log.Printf("[%s] Cleaning up TAP interface %s...", cfg.Name, cfg.TapDev)
			CleanupTapInterface(cfg.TapDev, guestIP)
		}()
	}

	// 2. Write configuration file for krun_worker
	configDir := "vms/run"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create run config directory: %w", err)
	}
	configPath := filepath.Join(configDir, fmt.Sprintf("%s_krun.json", cfg.Name))
	log.Printf("[%s] Writing VM configuration to %s...", cfg.Name, configPath)

	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to serialize VM config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write VM config: %w", err)
	}
	defer os.Remove(configPath)

	// 3. Spawns krun_worker process
	var workerPath string
	if execPath, err := os.Executable(); err == nil {
		// Try next to the current running executable
		workerPath = filepath.Join(filepath.Dir(execPath), "krun_worker")
	}
	if workerPath == "" || !fileExists(workerPath) {
		// Fallbacks
		workerPath = "./bin/krun_worker"
		if !fileExists(workerPath) {
			workerPath = "bin/krun_worker"
		}
	}

	log.Printf("[%s] Executing krun worker: %s %s...", cfg.Name, workerPath, configPath)
	cmd := exec.Command(workerPath, configPath)
	if cfg.Background {
		logFilePath := "vms/run/vm.log"
		logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open vm log file %s: %w", logFilePath, err)
		}
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		cmd.Stdin = nil
		defer logFile.Close()
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin // Bind stdin for interactive bash shell usage
	}

	// Programmatically pass the library path so libkrun.so can find libkrunfw.so.5
	binDir := filepath.Dir(workerPath)
	absBinDir, err := filepath.Abs(binDir)
	if err == nil {
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+absBinDir, "RUST_BACKTRACE=1")
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start krun worker: %w", err)
	}

	// 4. Wait for process exit or signal interrupts
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-stopChan:
		log.Printf("[%s] Service shutdown requested. Terminating microVM...", cfg.Name)
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGKILL)
		}
		<-done // Wait for cleanup exit
		return nil
	case sig := <-sigChan:
		log.Printf("[%s] Interrupted by signal (%v). Terminating microVM...", cfg.Name, sig)
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGKILL)
		}
		<-done // Wait for cleanup exit
		return nil
	case err := <-done:
		if err != nil {
			return fmt.Errorf("krun worker exited with error: %w", err)
		}
		log.Printf("[%s] MicroVM execution completed successfully.", cfg.Name)
		return nil
	}
}

// SetupTapInterface sets up host NAT routing and configures a TAP device.
func SetupTapInterface(tapName, hostIP, guestIP string) error {
	EnsureHostNAT()

	// Delete stale TAP interface if it exists
	exec.Command("ip", "link", "set", tapName, "down").Run()
	exec.Command("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run()

	// Create and bring up TAP interface
	if err := exec.Command("ip", "tuntap", "add", "dev", tapName, "mode", "tap").Run(); err != nil {
		return fmt.Errorf("ip tuntap add failed: %w", err)
	}
	if err := exec.Command("ip", "addr", "add", hostIP+"/32", "dev", tapName).Run(); err != nil {
		return fmt.Errorf("ip addr add failed: %w", err)
	}
	if err := exec.Command("ip", "link", "set", tapName, "up").Run(); err != nil {
		return fmt.Errorf("ip link set up failed: %w", err)
	}

	// Add static route to guest VM
	_ = exec.Command("ip", "route", "del", guestIP).Run()
	if err := exec.Command("ip", "route", "replace", guestIP, "dev", tapName).Run(); err != nil {
		return fmt.Errorf("ip route replace failed: %w", err)
	}

	return nil
}

// CleanupTapInterface tears down the TAP interface.
func CleanupTapInterface(tapName, guestIP string) {
	_ = exec.Command("ip", "link", "set", tapName, "down").Run()
	_ = exec.Command("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run()
	_ = exec.Command("ip", "route", "del", guestIP).Run()
}

// EnsureHostNAT sets up IP forwarding and iptables MASQUERADE.
func EnsureHostNAT() {
	_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()

	// Add iptables rules to allow NAT routing for guest VMs
	_ = exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", "172.16.0.0/24", "!", "-d", "172.16.0.0/24", "-j", "MASQUERADE").Run()
	_ = exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", "172.16.0.0/24", "!", "-d", "172.16.0.0/24", "-j", "MASQUERADE").Run()

	_ = exec.Command("iptables", "-D", "FORWARD", "-s", "172.16.0.0/24", "-j", "ACCEPT").Run()
	_ = exec.Command("iptables", "-I", "FORWARD", "-s", "172.16.0.0/24", "-j", "ACCEPT").Run()

	_ = exec.Command("iptables", "-D", "FORWARD", "-d", "172.16.0.0/24", "-j", "ACCEPT").Run()
	_ = exec.Command("iptables", "-I", "FORWARD", "-d", "172.16.0.0/24", "-j", "ACCEPT").Run()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
