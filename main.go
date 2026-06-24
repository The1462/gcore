package main

import (
	"archive/tar"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	needConfig := false
	if len(os.Args) < 2 {
		needConfig = true
	} else {
		switch os.Args[1] {
		case "setup", "nuke", "service", "run-vm", "prepare", "prepare-disks", "serve":
			needConfig = true
		}
	}

	if err := LoadConfig(); err != nil {
		if needConfig {
			fmt.Printf("Warning: Failed to load configuration: %v\n", err)
		}
	}

	prepareCmd := flag.NewFlagSet("prepare", flag.ExitOnError)
	targetDir := prepareCmd.String("dir", "vms/rootfs", "Target directory to export the rootfs to")

	prepareDisksCmd := flag.NewFlagSet("prepare-disks", flag.ExitOnError)
	appsDir := prepareDisksCmd.String("apps-dir", "vms/apps_source", "Source directory for apps")
	appsImg := prepareDisksCmd.String("apps-img", "vms/gcore_apps.img", "Output path for the apps ext4 image")
	appsSize := prepareDisksCmd.String("apps-size", "768M", "Size of the apps image (e.g. 768M)")
	configsDir := prepareDisksCmd.String("configs-dir", "vms/configs_source", "Source directory for configs")
	configsImg := prepareDisksCmd.String("configs-img", "vms/gcore_configs.img", "Output path for the configs ext4 image")
	configsSize := prepareDisksCmd.String("configs-size", "128M", "Size of the configs image (e.g. 128M)")
	storageImg := prepareDisksCmd.String("storage-img", "vms/gcore_storage.img", "Output path for the storage ext4 image")
	storageSize := prepareDisksCmd.String("storage-size", "1G", "Size of the storage image (e.g. 1G)")
	embedDir := prepareDisksCmd.String("embed", "embed/linux_amd64", "Path to gcore embed/linux_amd64 for binary copies")

	prepareSvcsCmd := flag.NewFlagSet("prepare-services", flag.ExitOnError)
	svcAppsDir := prepareSvcsCmd.String("apps-dir", "vms/apps_source", "Destination directory for service binaries")
	svcConfigsDir := prepareSvcsCmd.String("configs-dir", "vms/configs_source", "Destination directory for service configs")
	svcEmbedDir := prepareSvcsCmd.String("embed", "embed/linux_amd64", "Path to gcore embed/linux_amd64")

	runVMCmd := flag.NewFlagSet("run-vm", flag.ExitOnError)
	vmName := runVMCmd.String("name", "gcore-vm", "Name of the VM")
	vmCPUs := runVMCmd.Int("cpus", 1, "Number of vCPUs")
	vmMem := runVMCmd.Int("mem", 1536, "RAM size in MB")
	vmRootFS := runVMCmd.String("rootfs", "vms/rootfs", "Path to host rootfs directory")
	vmApps := runVMCmd.String("apps", "vms/gcore_apps.img", "Path to apps disk image")
	vmConfigs := runVMCmd.String("configs", "vms/gcore_configs.img", "Path to configs disk image")
	vmStorage := runVMCmd.String("storage", "vms/gcore_storage.img", "Path to storage disk image")
	vmTap := runVMCmd.String("tap", "tap_gcore", "Host TAP device name")

	serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
	servePort := serveCmd.Int("port", 17170, "HTTP port for the web setup UI")

	if len(os.Args) < 2 {
		handleAutoServiceManagement()
		os.Exit(0)
	}

	switch os.Args[1] {
	case "prepare":
		prepareCmd.Parse(os.Args[2:])
		if err := prepareOS(*targetDir); err != nil {
			fmt.Printf("Error preparing OS: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("OS preparation completed successfully!")
	case "prepare-services":
		prepareSvcsCmd.Parse(os.Args[2:])

		// Load config so service configs reflect current settings
		if err := LoadConfig(); err != nil {
			fmt.Printf("Warning: could not load config: %v\n", err)
		}
		cfgMutex.RLock()
		cfg := appConfig
		cfgMutex.RUnlock()

		fmt.Println("==> Copying service binaries...")
		if err := copyServiceBinaries(*svcEmbedDir, *svcAppsDir); err != nil {
			fmt.Printf("Error copying binaries: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("==> Generating service configs...")
		if err := generateServiceConfigs(*svcConfigsDir, cfg); err != nil {
			fmt.Printf("Error generating configs: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service preparation completed successfully!")
	case "prepare-disks":
		prepareDisksCmd.Parse(os.Args[2:])

		// Load config so config files reflect current settings
		if err := LoadConfig(); err != nil {
			fmt.Printf("Warning: could not load config: %v\n", err)
		}
		cfgMutex.RLock()
		cfg := appConfig
		cfgMutex.RUnlock()

		fmt.Println("==> Copying service binaries...")
		if err := copyServiceBinaries(*embedDir, *appsDir); err != nil {
			fmt.Printf("Warning: binary copy had errors: %v\n", err)
		}

		fmt.Println("==> Generating service configs...")
		if err := generateServiceConfigs(*configsDir, cfg); err != nil {
			fmt.Printf("Error generating configs: %v\n", err)
			os.Exit(1)
		}

		if err := prepareDisks(*appsDir, *appsImg, *appsSize, *configsDir, *configsImg, *configsSize); err != nil {
			fmt.Printf("Error preparing disk images: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("==> Preparing storage disk image...")
		if err := generateEmptyExt4Image(*storageImg, *storageSize); err != nil {
			fmt.Printf("Error preparing storage disk image: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Disk images preparation completed successfully!")
	case "setup":
		RunSetup()
	case "config":
		RunConfig(os.Args[2:])
	case "status":
		RunStatus()
	case "logs":
		RunLogs()
	case "serve":
		serveCmd.Parse(os.Args[2:])
		RunServe(*servePort, nil)
	case "nuke":
		RunNuke()
	case "service":
		if len(os.Args) < 3 {
			fmt.Println("Expected service action: install, uninstall, start, stop, restart, status, run")
			os.Exit(1)
		}
		RunService(os.Args[2])
	case "run-vm":
		runVMCmd.Parse(os.Args[2:])

		var token string
		var setupErr error
		cfgMutex.RLock()
		hasCF := appConfig.CFDomain != "" && appConfig.CFApiToken != ""
		cfgMutex.RUnlock()

		if hasCF {
			fmt.Println("Cloudflare Domain configured. Setting up Cloudflare Tunnel...")
			token, setupErr = SetupCloudflareTunnel()
			if setupErr != nil {
				fmt.Printf("Warning: Cloudflare tunnel setup failed: %v\n", setupErr)
			} else {
				fmt.Println("✓ Cloudflare Tunnel configured successfully.")
			}
		}

		var execEnv []string
		var guestCmd string

		cfgMutex.RLock()
		cfgCopy := appConfig
		cfgMutex.RUnlock()

		guestCmd = generateGuestInitScript(cfgCopy)

		if token != "" {
			execEnv = []string{
				"CLOUDFLARE_TOKEN=" + token,
				"PATH=/bin:/usr/bin:/sbin:/usr/sbin",
				"TERM=xterm",
			}
		}

		execArgs := []string{
			"/bin/bash",
			"-c",
			guestCmd,
		}

		vmCfg := VMRunnerConfig{
			Name:        *vmName,
			VCPUs:       *vmCPUs,
			MemMB:       *vmMem,
			RootFS:      *vmRootFS,
			AppsDisk:    *vmApps,
			ConfigsDisk: *vmConfigs,
			StorageDisk: *vmStorage,
			ExecArgs:    execArgs,
			ExecEnv:     execEnv,
			TapDev:      *vmTap,
		}

		if err := StartVM(vmCfg, nil); err != nil {
			fmt.Printf("Error running VM: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

// getContainerEngine detects if docker or podman is available in PATH.
func getContainerEngine() (string, error) {
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker", nil
	}
	if _, err := exec.LookPath("podman"); err == nil {
		return "podman", nil
	}
	return "", fmt.Errorf("neither 'docker' nor 'podman' executable was found in PATH")
}

// prepareOS builds the Docker image and exports it as a rootfs directory for libkrun.
func prepareOS(targetDir string) error {
	engine, err := getContainerEngine()
	if err != nil {
		return err
	}

	imageName := "gcore-os:latest"
	dockerfilePath := "vms/Dockerfile.gcore"

	fmt.Printf("Building container image %s using %s from %s...\n", imageName, engine, dockerfilePath)
	cmd := exec.Command(engine, "build", "-t", imageName, "-f", dockerfilePath, "vms")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to build container image: %w", err)
	}

	fmt.Println("Creating temporary container...")
	containerName := "gcore-os-temp"
	// Ensure any stale temporary container is removed first
	exec.Command(engine, "rm", "-f", containerName).Run()

	cmd = exec.Command(engine, "create", "--name", containerName, imageName, "true")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create temporary container: %w", err)
	}
	defer func() {
		fmt.Println("Cleaning up temporary container...")
		exec.Command(engine, "rm", "-f", containerName).Run()
	}()

	fmt.Printf("Exporting container filesystem to %s...\n", targetDir)
	if err := os.RemoveAll(targetDir); err != nil {
		return fmt.Errorf("failed to clean target directory: %w", err)
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// We stream the export directly to our unpacker instead of writing a temporary tar file on disk.
	exportCmd := exec.Command(engine, "export", containerName)
	stdout, err := exportCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe for container export: %w", err)
	}

	if err := exportCmd.Start(); err != nil {
		return fmt.Errorf("failed to start container export: %w", err)
	}

	if err := untar(stdout, targetDir); err != nil {
		return fmt.Errorf("failed to untar rootfs: %w", err)
	}

	if err := exportCmd.Wait(); err != nil {
		return fmt.Errorf("container export failed: %w", err)
	}

	return nil
}

// isSafePath checks that the target path does not resolve to a location outside targetDir.
func isSafePath(targetDir, path string) (bool, error) {
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return false, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}

	// We resolve all symlinks up to the parent directory because the file/symlink itself
	// might not exist yet.
	parent := filepath.Dir(absPath)
	evalParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		// Parent doesn't exist yet, so we walk up to the longest existing ancestor
		curr := parent
		for {
			eval, err := filepath.EvalSymlinks(curr)
			if err == nil {
				evalParent = eval
				break
			}
			parentDir := filepath.Dir(curr)
			if parentDir == curr {
				break
			}
			curr = parentDir
		}
	}

	rel, err := filepath.Rel(absTarget, evalParent)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false, nil
	}
	return true, nil
}

// untar extracts a tar stream to a destination directory.
func untar(reader io.Reader, targetDir string) error {
	tarReader := tar.NewReader(reader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Clean up path to prevent zip slip vulnerability
		path := filepath.Clean(header.Name)
		if filepath.IsAbs(path) || path == ".." || filepath.HasPrefix(path, "../") {
			continue
		}

		target := filepath.Join(targetDir, path)

		// Basic Zip Slip check
		safe, err := isSafePath(targetDir, target)
		if err != nil || !safe {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, header.FileInfo().Mode())
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		case tar.TypeSymlink:
			// Create symlink
			if err := os.Symlink(header.Linkname, target); err != nil {
				// Symlinks might fail if they exist or points to target not yet created, but we proceed
				continue
			}
		case tar.TypeLink:
			// Hardlink
			oldPath := filepath.Join(targetDir, header.Linkname)
			safeOld, _ := isSafePath(targetDir, oldPath)
			if !safeOld {
				continue
			}
			if err := os.Link(oldPath, target); err != nil {
				continue
			}
		}
	}
	return nil
}

// prepareDisks handles the creation and population of both apps and configs ext4 disk images.
func prepareDisks(appsDir, appsImg, appsSize, configsDir, configsImg, configsSize string) error {
	fmt.Printf("Preparing apps disk image at %s (source: %s, size: %s)...\n", appsImg, appsDir, appsSize)
	if err := generateExt4Image(appsDir, appsImg, appsSize); err != nil {
		return fmt.Errorf("failed to generate apps disk image: %w", err)
	}

	fmt.Printf("Preparing configs disk image at %s (source: %s, size: %s)...\n", configsImg, configsDir, configsSize)
	if err := generateExt4Image(configsDir, configsImg, configsSize); err != nil {
		return fmt.Errorf("failed to generate configs disk image: %w", err)
	}

	return nil
}

// generateExt4Image generates a formatted ext4 image from a source directory.
func generateExt4Image(sourceDir, imgPath, sizeStr string) error {
	// 1. Ensure source directory exists
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		return fmt.Errorf("failed to create source directory %s: %w", sourceDir, err)
	}

	// 2. Ensure parent directory of output image exists
	if err := os.MkdirAll(filepath.Dir(imgPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory for %s: %w", imgPath, err)
	}

	// Remove old image if it exists
	_ = os.Remove(imgPath)

	// 3. Create sparse file using truncate command
	fmt.Printf("Creating sparse file %s with size %s...\n", imgPath, sizeStr)
	cmd := exec.Command("truncate", "-s", sizeStr, imgPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to truncate sparse file: %w", err)
	}

	// 4. Format and copy source files using mkfs.ext4 -d
	fmt.Printf("Formatting %s as ext4 and importing files from %s...\n", imgPath, sourceDir)
	cmd = exec.Command("mkfs.ext4", "-F", "-d", sourceDir, imgPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mkfs.ext4 failed: %w", err)
	}

	return nil
}

// generateEmptyExt4Image generates a formatted blank ext4 image.
func generateEmptyExt4Image(imgPath, sizeStr string) error {
	// 1. Ensure parent directory of output image exists
	if err := os.MkdirAll(filepath.Dir(imgPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory for %s: %w", imgPath, err)
	}

	// Remove old image if it exists
	_ = os.Remove(imgPath)

	// 2. Create sparse file using truncate command
	fmt.Printf("Creating sparse file %s with size %s...\n", imgPath, sizeStr)
	cmd := exec.Command("truncate", "-s", sizeStr, imgPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to truncate sparse file: %w", err)
	}

	// 3. Format as ext4
	fmt.Printf("Formatting %s as ext4...\n", imgPath)
	cmd = exec.Command("mkfs.ext4", "-F", imgPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mkfs.ext4 failed: %w", err)
	}

	return nil
}
