#!/bin/bash
set -e

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_DIR="$( dirname "$DIR" )"

echo "Building libkrun builder container..."
podman build -t gcore-libkrun-builder -f "$DIR/Dockerfile.libkrun" "$DIR"

echo "Extracting compiled libkrun to $PROJECT_DIR/libkrun_out..."
podman create --name libkrun-extract gcore-libkrun-builder
rm -rf "$PROJECT_DIR/libkrun_out"
podman cp libkrun-extract:/build_out "$PROJECT_DIR/libkrun_out"
podman rm libkrun-extract

echo "Done! Compiled libkrun extracted to $PROJECT_DIR/libkrun_out."
