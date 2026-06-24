# gcore

Welcome to **gcore**, a core virtualization and isolation runtime system.

---

## 🛠️ Development & Build Cycle

> [!IMPORTANT]
> **Rule for Future AI Agents & Developers:**
> All build environment cycles, setup, and dependency management MUST be triggered via the `Makefile`. Do not introduce side-channel build scripts or manual installation steps that bypass the `Makefile`.

### Makefile Targets

Your `Makefile` must implement and maintain the following key targets:

1. **`make init`**
   - Downloads, clones, compiles, and links external library dependencies (e.g., `libkrun`) that are required to build or run the binary.
   
2. **`make build`**
   - Compiles the Golang application.

3. **`make prepare-os`**
   - Compiles the Golang application, builds the guest OS image from [vms/Dockerfile.gcore](vms/Dockerfile.gcore) using Docker or Podman, and extracts its root filesystem to `vms/rootfs` for libkrun.

4. **`make clean`**
   - Cleans build artifacts and temporary files.
   - **Crucially**, it must delete external requirements fetched during `make init` (like cloned repositories and built libraries such as `libkrun`) and the extracted `vms/rootfs` directory. This ensures that running `make clean` followed by `make init` allows compiling against newer versions of the external libraries in a clean environment.

5. **`make nuke`**
   - Performs a complete cleanup of everything set up by the application on the host system.
   - Must stop and kill any running services (including temporary containers like `gcore-os-temp`), clean up configuration files, delete cache/log files, and remove system-level modifications created or managed by this application.

### ⚠️ Critical Rule for Extending the Project
When adding new external libraries, system services, or configuration files to the project, **you must update the corresponding `Makefile` functions** (`init`, `clean`, and `nuke`) to account for these additions. This keeps the build and cleanup environment deterministic for both humans and future AI agents.

---

## 🚀 Getting Started

### Prerequisites

Ensure your host system has the necessary build tools and kernel configurations:
- `make`
- Golang toolchain
- Container runtime (`docker` or `podman`) to build and export the rootfs
- C/C++ compiler and tools (if compiling `libkrun` or other C dependencies)
- KVM access (e.g., `/dev/kvm` permissions)

### Installation Flow

To initialize the environment and fetch all required dependencies:
```bash
make init
```

To build the project binaries:
```bash
make build
```

To prepare the guest VM OS rootfs:
```bash
make prepare-os
```

To run the application:
```bash
make run
```

---

## 📁 Directory Structure

- [vms/Dockerfile.gcore](vms/Dockerfile.gcore): Defines the guest operating system (based on Alpine Linux) running inside the microVM.
- [main.go](main.go): Main entry point for the Golang application, including the orchestration command (`prepare`) to build the container image and unpack the rootfs stream into `vms/rootfs`.

---

## 🧹 Cleanup Procedures

To clean project artifacts and reset external dependencies for a clean rebuild:
```bash
make clean
```

To completely uninstall, kill all active services, and wipe host modifications:
```bash
make nuke
```
