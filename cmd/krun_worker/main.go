package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/mishushakov/libkrun-go/krun"
)

type VMConfig struct {
	Name        string
	VCPUs       int
	MemMB       int
	RootFS      string
	AppsDisk    string
	ConfigsDisk string
	StorageDisk string
	ExecArgs    []string
	ExecEnv     []string
	TapDev      string
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("krun_worker requires a config path")
	}
	configPath := os.Args[1]

	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Worker failed to read config: %v", err)
	}

	var cfg VMConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("Worker failed to parse config: %v", err)
	}
	log.Printf("Parsed config: %+v", cfg)

	ctx, err := krun.CreateContext()
	if err != nil {
		log.Fatalf("Worker failed to create krun context: %v", err)
	}
	defer ctx.Free()

	if cfg.TapDev == "" {
		log.Printf("Using TSI user-mode networking")
		if err := ctx.DisableImplicitVsock(); err != nil {
			log.Printf("Worker Warning: failed to disable implicit vsock: %v", err)
		}
		if err := ctx.AddVsock(krun.TSIHijackInet); err != nil {
			log.Fatalf("Worker failed to add vsock with TSI: %v", err)
		}
		if err := ctx.SetPortMap(nil); err != nil {
			log.Printf("Worker Warning: failed to set port map: %v", err)
		}
	}

	// Configure CPU and RAM
	if err := ctx.SetVMConfig(krun.VMConfig{
		NumVCPUs: uint8(cfg.VCPUs),
		RAMMiB:   uint32(cfg.MemMB),
	}); err != nil {
		log.Fatalf("Worker failed to set VM config: %v", err)
	}

	// Configure host-shared directory as rootfs
	if err := ctx.SetRoot(cfg.RootFS); err != nil {
		log.Fatalf("Worker failed to set rootfs: %v", err)
	}

	// Add secondary disks (Apps and Configs) if specified
	// Note: in libkrun, block devices are mapped inside the VM in order: /dev/vda, /dev/vdb, etc.
	if cfg.AppsDisk != "" {
		log.Printf("Attaching apps disk image: %s (/dev/vda)", cfg.AppsDisk)
		if err := ctx.AddDisk(krun.DiskConfig{
			BlockID:  "apps",
			Path:     cfg.AppsDisk,
			Format:   krun.DiskFormatRaw,
			ReadOnly: true,
		}); err != nil {
			log.Fatalf("Worker failed to add apps disk: %v", err)
		}
	}

	if cfg.ConfigsDisk != "" {
		log.Printf("Attaching configs disk image: %s (/dev/vdb)", cfg.ConfigsDisk)
		if err := ctx.AddDisk(krun.DiskConfig{
			BlockID:  "configs",
			Path:     cfg.ConfigsDisk,
			Format:   krun.DiskFormatRaw,
			ReadOnly: true,
		}); err != nil {
			log.Fatalf("Worker failed to add configs disk: %v", err)
		}
	}

	if cfg.StorageDisk != "" {
		log.Printf("Attaching storage disk image: %s (/dev/vdc)", cfg.StorageDisk)
		if err := ctx.AddDisk(krun.DiskConfig{
			BlockID:  "storage",
			Path:     cfg.StorageDisk,
			Format:   krun.DiskFormatRaw,
			ReadOnly: false,
		}); err != nil {
			log.Fatalf("Worker failed to add storage disk: %v", err)
		}
	}

	// Configure Network (TAP device integration)
	if cfg.TapDev != "" {
		log.Printf("Attaching network TAP device: %s", cfg.TapDev)
		if err := ctx.AddNetTap(krun.NetTapConfig{
			TapName:  cfg.TapDev,
			MAC:      [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
			Features: krun.NetFeatureCsum | krun.NetFeatureHostTSO4 | krun.NetFeatureHostTSO6 | krun.NetFeatureHostUFO,
			Flags:    0,
		}); err != nil {
			log.Printf("Worker Warning: failed to configure tap device %s: %v", cfg.TapDev, err)
		}
	}

	// Configure Guest Command Exec
	env := cfg.ExecEnv
	if len(env) == 0 {
		env = []string{"PATH=/bin:/usr/bin:/sbin:/usr/sbin", "TERM=xterm"}
	}

	if len(cfg.ExecArgs) > 0 {
		if err := ctx.SetExec(krun.ExecConfig{
			Path: cfg.ExecArgs[0],
			Args: cfg.ExecArgs[1:],
			Env:  env,
		}); err != nil {
			log.Fatalf("Worker failed to set exec config: %v", err)
		}
	} else {
		// Default fallback shell entrypoint
		if err := ctx.SetExec(krun.ExecConfig{
			Path: "/bin/bash",
			Args: []string{},
			Env:  env,
		}); err != nil {
			log.Fatalf("Worker failed to set default exec: %v", err)
		}
	}

	log.Printf("Starting libkrun microVM %s...", cfg.Name)
	if err := ctx.StartEnter(); err != nil {
		log.Fatalf("Worker krun StartEnter failed: %v", err)
	}
	os.Exit(0)
}
