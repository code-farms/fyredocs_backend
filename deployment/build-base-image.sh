#!/bin/bash

# Build the fyredocs-base image with LibreOffice
# This only needs to be done once, or when updating Alpine/LibreOffice versions

set -e

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(cd "$SCRIPT_DIR/.." && pwd)
cd "$ROOT_DIR"

echo "Building fyredocs-base image with LibreOffice..."
echo ""

# Build the base image and tag it as fyredocs-base:latest
docker build \
    -f deployment/base-alpine-libreoffice.Dockerfile \
    -t fyredocs-base:latest \
    .

echo ""
echo "Base image built successfully!"
echo ""
echo "To test the image locally:"
echo "  docker run --rm fyredocs-base:latest libreoffice --version"
echo ""
echo "Now you can run ./deployment/deploy.sh to build all services."
echo ""
