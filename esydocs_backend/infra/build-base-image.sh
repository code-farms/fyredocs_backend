#!/bin/bash

# Build and push base image with LibreOffice
# This only needs to be done once, or when updating Alpine/LibreOffice versions

set -e

# Configuration - UPDATE THESE VALUES
REGISTRY="docker.io"  # or your registry: ghcr.io, your-registry.com
USERNAME="your-dockerhub-username"  # your Docker Hub or registry username
IMAGE_NAME="alpine-libreoffice"
TAG="3.19"

FULL_IMAGE="${REGISTRY}/${USERNAME}/${IMAGE_NAME}:${TAG}"

echo "Building base image with LibreOffice..."
echo "Image: ${FULL_IMAGE}"
echo ""

# Build the base image
docker build \
    -f infra/base-alpine-libreoffice.Dockerfile \
    -t "${FULL_IMAGE}" \
    -t "${REGISTRY}/${USERNAME}/${IMAGE_NAME}:latest" \
    .

echo ""
echo "✓ Base image built successfully!"
echo ""
echo "To test the image locally:"
echo "  docker run --rm ${FULL_IMAGE} libreoffice --version"
echo ""
echo "To push to registry (requires login):"
echo "  docker login ${REGISTRY}"
echo "  docker push ${FULL_IMAGE}"
echo "  docker push ${REGISTRY}/${USERNAME}/${IMAGE_NAME}:latest"
echo ""
echo "To use in convert-from-pdf service:"
echo "  docker build --build-arg BASE_IMAGE=${FULL_IMAGE} -f convert-from-pdf/Dockerfile ."
