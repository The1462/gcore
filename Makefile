# Makefile for gcore

# Go configuration
BINARY_NAME=gcore
ROOTFS_DIR=vms/rootfs

.PHONY: all init build build-libkrun fetch-libkrun prepare-os prepare-services prepare-disks clean nuke run-vm release

all: build

# 1. make init: Setup build environment and dependencies
init:
	@echo "==> Initializing build environment..."
	go mod tidy
	@echo "==> Checking external service dependencies..."
	@mkdir -p embed/linux_amd64
	@if [ -d ../genesis_core/embed/linux_amd64 ]; then \
		echo "Copying existing service binaries from sibling directory..."; \
		cp -n ../genesis_core/embed/linux_amd64/* embed/linux_amd64/ 2>/dev/null || true; \
	fi
	@if [ ! -f embed/linux_amd64/rustfs ]; then \
		echo "Downloading rustfs v1.0.0-beta.8..."; \
		curl -L -o /tmp/rustfs.zip https://github.com/rustfs/rustfs/releases/download/1.0.0-beta.8/rustfs-linux-x86_64-musl-v1.0.0-beta.8.zip; \
		unzip -o /tmp/rustfs.zip -d /tmp/rustfs_extracted; \
		cp /tmp/rustfs_extracted/rustfs embed/linux_amd64/; \
		chmod +x embed/linux_amd64/rustfs; \
		rm -rf /tmp/rustfs.zip /tmp/rustfs_extracted; \
	fi

# 2. make build-libkrun: Build libkrun inside a container
build-libkrun:
	@echo "==> Building libkrun native C library inside container..."
	./scripts/build_libkrun.sh

# 2b. make fetch-libkrun: Fetch pre-compiled libkrun release from GitHub
fetch-libkrun:
	@echo "==> Fetching pre-compiled libkrun release..."
	@ARCH=$$(uname -m); \
	if [ "$$ARCH" = "x86_64" ]; then \
		FILE="libkrun-x86_64.tar.gz"; \
	elif [ "$$ARCH" = "aarch64" ]; then \
		FILE="libkrun-aarch64.tar.gz"; \
	else \
		echo "Unsupported architecture: $$ARCH" && exit 1; \
	fi; \
	URL="https://github.com/The1462/libkrun/releases/download/v1.19.1-gcore/$$FILE"; \
	echo "Downloading $$URL..."; \
	rm -rf libkrun_out/; \
	mkdir -p libkrun_out/; \
	curl -L -s -o /tmp/$$FILE $$URL; \
	tar -xzf /tmp/$$FILE -C libkrun_out/; \
	rm -f /tmp/$$FILE
	@echo "==> Pre-compiled libkrun successfully extracted to libkrun_out/."


# 3. make build: Compile the gcore application and krun_worker helper
build: init
	@echo "==> Building Go binary..."
	@mkdir -p bin
	CGO_ENABLED=0 go build -o bin/$(BINARY_NAME) .
	@if [ -d "libkrun_out" ]; then \
		echo "Found libkrun_out, building krun_worker with CGO enabled for libkrun..."; \
		cp -r $$(pwd)/libkrun_out/lib64/* ./bin/; \
		CGO_ENABLED=1 CGO_CFLAGS="-I$$(pwd)/libkrun_out/include" CGO_LDFLAGS="-L$$(pwd)/bin -lkrun -Wl,-rpath,\$$ORIGIN" go build -tags krun_blk,krun_net -o bin/krun_worker ./cmd/krun_worker/main.go; \
	fi

# 4. make prepare-os: Build the Docker OS image and export rootfs
prepare-os: build
	@echo "==> Preparing OS image and rootfs for libkrun..."
	./bin/$(BINARY_NAME) prepare --dir=$(ROOTFS_DIR)

# 4b. make prepare-services: Copy binaries and generate configs for all services
prepare-services: build
	@echo "==> Preparing service binaries and configs..."
	./bin/$(BINARY_NAME) prepare-services \
		--apps-dir=vms/apps_source \
		--configs-dir=vms/configs_source \
		--embed=embed/linux_amd64

# 5. make prepare-disks: Build the secondary ext4 filesystem disk images (includes services)
prepare-disks: build
	@echo "==> Preparing secondary disk images (includes binary copy + config generation)..."
	./bin/$(BINARY_NAME) prepare-disks \
		--apps-dir=vms/apps_source \
		--apps-img=vms/gcore_apps.img \
		--apps-size=768M \
		--configs-dir=vms/configs_source \
		--configs-img=vms/gcore_configs.img \
		--configs-size=128M \
		--storage-img=vms/gcore_storage.img \
		--storage-size=1G \
		--embed=embed/linux_amd64

# 6. make run-vm: Launch a test microVM (requires sudo)
run-vm: build
	@echo "==> Launching test VM..."
	sudo ./bin/$(BINARY_NAME) run-vm

# 7. make clean: Clean up artifact files and dependencies fetched via make init
clean:
	@echo "==> Cleaning local artifacts..."
	rm -rf bin/
	rm -rf dist/
	rm -rf $(ROOTFS_DIR)
	rm -f vms/gcore_apps.img
	rm -f vms/gcore_configs.img
	rm -f vms/gcore_storage.img
	rm -f embed/linux_amd64/rustfs
	rm -rf libkrun_out/
	rm -rf third_party/

# 8. make nuke: Stop/kill services, wipe configurations, clean setup on host
nuke:
	@echo "==> Nuking host setup and services..."
	-sudo ./bin/$(BINARY_NAME) nuke
	-docker rm -f gcore-os-temp 2>/dev/null || true
	-podman rm -f gcore-os-temp 2>/dev/null || true
	$(MAKE) clean
	@echo "==> Host system cleaned and services terminated."

# 9. make release: Build cross-compiled binaries and package guest OS rootfs
release: prepare-os
	@echo "==> Building release binaries for multiple platforms..."
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/gcore-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o dist/gcore-linux-arm64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o dist/gcore-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o dist/gcore-darwin-arm64 .
	@echo "==> Packaging guest OS rootfs image..."
	@if [ -d "vms/rootfs" ]; then \
		tar -czf dist/gcore-rootfs.tar.gz -C vms rootfs; \
		echo "RootFS packaged: dist/gcore-rootfs.tar.gz"; \
	else \
		echo "Warning: vms/rootfs does not exist. Run 'make prepare-os' first."; \
	fi

