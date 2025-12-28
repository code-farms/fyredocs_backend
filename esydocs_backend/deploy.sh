#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

cd "$ROOT_DIR"

echo "Starting essentials (db, redis)..."
docker compose -f docker-compose.essentials.yml up -d

echo "Starting upload-service..."
docker compose -f docker-compose-upload.yml up -d --build

echo "Starting convert-from-pdf..."
docker compose -f docker-compose-convert-from-pdf.yml up -d --build

echo "Starting convert-to-pdf..."
docker compose -f docker-compose-convert-to-pdf.yml up -d --build

echo "All services started."
