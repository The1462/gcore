package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Version is the build tag, set by compile flags.
var Version = ""

func getVersion() string {
	if Version != "" {
		return Version
	}
	// Try fetching the latest tag dynamically from GitHub
	tag, err := getLatestGcoreReleaseTag()
	if err == nil && tag != "" {
		return tag
	}
	return "v1.0.0"
}

func getLatestGcoreReleaseTag() (string, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.github.com/repos/The1462/gcore/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "gcore-updater")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status: %s", resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

// DownloadAndExtractDependencies downloads libkrun, krun_worker, and the rootfs dynamically.
func DownloadAndExtractDependencies() error {
	if runtime.GOOS != "linux" {
		log.Println("[downloader] Skipping dependency downloads since virtualization is only supported on Linux.")
		return nil
	}

	arch := runtime.GOARCH
	var libkrunArch string
	if arch == "amd64" {
		libkrunArch = "x86_64"
	} else if arch == "arm64" {
		libkrunArch = "aarch64"
	} else {
		return fmt.Errorf("unsupported virtualization architecture: %s", arch)
	}

	// 1. Ensure libkrun native library is available
	libkrunDir := "bin"
	libkrunPath := filepath.Join(libkrunDir, "libkrun.so.1")
	if _, err := os.Stat(libkrunPath); os.IsNotExist(err) {
		log.Println("[downloader] libkrun.so.1 not found. Downloading libkrun precompiled package...")
		url := fmt.Sprintf("https://github.com/The1462/libkrun/releases/download/v1.19.1-gcore/libkrun-%s.tar.gz", libkrunArch)
		tmpTar := filepath.Join(os.TempDir(), fmt.Sprintf("libkrun-%s.tar.gz", libkrunArch))
		
		if err := downloadFile(url, tmpTar); err != nil {
			return fmt.Errorf("failed to download libkrun: %w", err)
		}
		defer os.Remove(tmpTar)

		tmpExtractDir := filepath.Join(os.TempDir(), "libkrun_extracted")
		_ = os.RemoveAll(tmpExtractDir)
		if err := os.MkdirAll(tmpExtractDir, 0755); err != nil {
			return err
		}
		defer os.RemoveAll(tmpExtractDir)

		if err := extractTarGz(tmpTar, tmpExtractDir); err != nil {
			return fmt.Errorf("failed to extract libkrun: %w", err)
		}

		// Copy contents of lib64/ to bin/
		extractedLibDir := filepath.Join(tmpExtractDir, "lib64")
		if _, err := os.Stat(extractedLibDir); os.IsNotExist(err) {
			// Fallback if structured differently
			extractedLibDir = tmpExtractDir
		}

		if err := os.MkdirAll(libkrunDir, 0755); err != nil {
			return err
		}

		files, err := os.ReadDir(extractedLibDir)
		if err != nil {
			return err
		}

		for _, file := range files {
			src := filepath.Join(extractedLibDir, file.Name())
			dst := filepath.Join(libkrunDir, file.Name())
			
			// Resolve symlinks or copy directly
			if file.Type()&os.ModeSymlink != 0 {
				linkTarget, err := os.Readlink(src)
				if err == nil {
					_ = os.Remove(dst)
					_ = os.Symlink(linkTarget, dst)
				}
			} else {
				if err := copyFile(src, dst); err != nil {
					return fmt.Errorf("failed to copy dynamic library file: %w", err)
				}
			}
		}
		
		// Copy includes for local compilation fallback support
		extractedIncDir := filepath.Join(tmpExtractDir, "include")
		if _, err := os.Stat(extractedIncDir); err == nil {
			_ = copyDir(extractedIncDir, "libkrun_out/include")
		}

		log.Println("[downloader] libkrun library downloaded and configured successfully.")
	}

	// 2. Ensure krun_worker helper binary is available
	workerPath := "bin/krun_worker"
	if _, err := os.Stat(workerPath); os.IsNotExist(err) {
		log.Println("[downloader] krun_worker not found. Attempting download from release...")
		version := getVersion()
		url := fmt.Sprintf("https://github.com/The1462/gcore/releases/download/%s/krun_worker-linux-%s", version, arch)
		
		if err := downloadFile(url, workerPath); err != nil {
			log.Printf("[downloader] Release download failed (%v). Checking for local source compilation fallback...", err)
			if _, statErr := os.Stat("cmd/krun_worker/main.go"); statErr == nil {
				log.Println("[downloader] Local source code found. Compiling krun_worker locally...")
				if compileErr := compileKrunWorkerLocal(); compileErr != nil {
					return fmt.Errorf("local compilation failed: %w", compileErr)
				}
			} else {
				return fmt.Errorf("krun_worker binary not found and local source code is unavailable: %w", err)
			}
		} else {
			_ = os.Chmod(workerPath, 0755)
			log.Println("[downloader] krun_worker helper downloaded successfully.")
		}
	}

	// 3. Ensure Guest OS rootfs is available
	rootfsPath := "vms/rootfs"
	if _, err := os.Stat(rootfsPath); os.IsNotExist(err) {
		log.Println("[downloader] Guest OS rootfs not found. Downloading precompiled rootfs package...")
		version := getVersion()
		url := fmt.Sprintf("https://github.com/The1462/gcore/releases/download/%s/gcore-rootfs.tar.gz", version)
		tmpTar := filepath.Join(os.TempDir(), "gcore-rootfs.tar.gz")

		if err := downloadFile(url, tmpTar); err != nil {
			return fmt.Errorf("failed to download guest rootfs: %w", err)
		}
		defer os.Remove(tmpTar)

		if err := os.MkdirAll("vms", 0755); err != nil {
			return err
		}

		if err := extractTarGz(tmpTar, "vms"); err != nil {
			return fmt.Errorf("failed to extract guest rootfs: %w", err)
		}

		log.Println("[downloader] Guest OS rootfs installed successfully.")
	}

	return nil
}

func downloadFile(url, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gcore-downloader")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s for url: %s", resp.Status, url)
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func extractTarGz(tarGzPath, destDir string) error {
	file, err := os.Open(tarGzPath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	return untar(gzipReader, destDir)
}

func compileKrunWorkerLocal() error {
	cmd := exec.Command("go", "build", "-tags", "krun_blk,krun_net", "-o", "bin/krun_worker", "./cmd/krun_worker")
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=1",
		"CGO_CFLAGS=-I"+resolvePath("libkrun_out/include"),
		"CGO_LDFLAGS=-L"+resolvePath("bin")+" -lkrun -Wl,-rpath,$ORIGIN",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDir(src string, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}
