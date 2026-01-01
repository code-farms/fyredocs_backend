#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

cd "$ROOT_DIR"

echo "Stopping existing containers..."
docker compose -f docker-compose.essentials.yml \
  -f docker-compose-upload.yml \
  -f docker-compose-convert-from-pdf.yml \
  -f docker-compose-convert-to-pdf.yml \
  -f docker-compose-gateway.yml \
  down --remove-orphans

echo "Starting essentials (db, redis)..."
docker compose -f docker-compose.essentials.yml up -d

echo "Starting upload-service..."
docker compose -f docker-compose-upload.yml up -d --build

echo "Starting convert-from-pdf..."
docker compose -f docker-compose-convert-from-pdf.yml up -d --build

echo "Starting convert-to-pdf..."
docker compose -f docker-compose-convert-to-pdf.yml up -d --build

echo "Starting traefik..."
docker compose -f docker-compose-gateway.yml up -d --build

echo "All services started."