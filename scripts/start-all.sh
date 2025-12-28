#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

cd "$ROOT_DIR"

echo "Starting essentials (db, redis)..."
docker compose -f docker-compose.essentials.yml up -d

echo "Starting backend..."
docker compose -f esydocs_backend/docker-compose.yml up -d --build

echo "Starting frontend..."
docker compose -f esydocs_frontend/docker-compose.yml up -d --build

echo "All services started."
