#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

cd "$ROOT_DIR"

echo ""
echo "=============================================================="
echo "============== Stopping existing containers... ==============="
echo "=============================================================="
docker compose -f docker-compose.essentials.yml \
  -f docker-compose-upload.yml \
  -f docker-compose-convert-from-pdf.yml \
  -f docker-compose-convert-to-pdf.yml \
  -f docker-compose-cleanup.yml \
  -f docker-compose-gateway.yml \
  down --remove-orphans

echo ""
echo "=============================================================="
echo "============= Starting essentials (db, redis)... ============="
echo "=============================================================="
docker compose -f docker-compose.essentials.yml up -d

echo ""
echo "=============================================================="
echo "============== Starting upload-service... ===================="
echo "=============================================================="
docker compose -f docker-compose-upload.yml up -d --build

echo ""
echo "=============================================================="
echo "=============== Starting convert-from-pdf... ================"
echo "=============================================================="
docker compose -f docker-compose-convert-from-pdf.yml up -d --build

echo ""
echo "=============================================================="
echo "================ Starting convert-to-pdf... =================="
echo "=============================================================="
docker compose -f docker-compose-convert-to-pdf.yml up -d --build

echo ""
echo "=============================================================="
echo "================= Starting cleanup worker... ================="
echo "=============================================================="
docker compose -f docker-compose-cleanup.yml up -d --build

echo ""
echo "=============================================================="
echo "================== Starting api-gateway... ==================="
echo "=============================================================="
docker compose -f docker-compose-gateway.yml up -d --build

echo ""
echo "=============================================================="
echo "=================== All services started. ===================="
echo "=============================================================="
