#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$ROOT_DIR"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored messages
print_step() {
    echo ""
    echo -e "${BLUE}=============================================================="
    echo -e "============== $1 ============="
    echo -e "==============================================================${NC}"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

# Check if openssl is available
if ! command -v openssl &> /dev/null; then
    print_error "openssl is required but not installed. Please install openssl first."
    exit 1
fi

# Generate or load JWT secret
JWT_SECRET_FILE=".jwt_secret"
if [ -f "$JWT_SECRET_FILE" ]; then
    print_warning "Found existing JWT secret in $JWT_SECRET_FILE"
    export JWT_HS256_SECRET=$(cat "$JWT_SECRET_FILE")
    print_success "Loaded JWT secret from file"
else
    print_step "Generating new JWT secret..."
    export JWT_HS256_SECRET=$(openssl rand -hex 32)
    echo "$JWT_HS256_SECRET" > "$JWT_SECRET_FILE"
    chmod 600 "$JWT_SECRET_FILE"
    print_success "Generated and saved new JWT secret to $JWT_SECRET_FILE"
fi

# Configuration Notice
print_step "PDF Processing Configuration"
print_success "Using pre-built base image: esydocs-base:latest"
print_success "Using Go Workspace caching for ultra-fast builds"

# 1. Stop existing containers
print_step "Stopping existing containers..."
docker compose down --remove-orphans 2>/dev/null || true
print_success "Stopped existing containers"

# 2. Sequential Build Stage (CPU-Safe Mode)
print_step "Building Go Services sequentially..."

# We build them one-by-one to keep the CPU Load Average low
GO_SERVICES=(
  "api-gateway" 
  "auth-service" 
  "job-service" 
  "convert-from-pdf" 
  "convert-to-pdf" 
  "organize-pdf" 
  "optimize-pdf" 
  "cleanup-worker"
)

# Enable BuildKit for advanced caching
export DOCKER_BUILDKIT=1

for SERVICE in "${GO_SERVICES[@]}"; do
    echo -e "${YELLOW}🔨 Starting build for: $SERVICE...${NC}"
    START_TIME=$SECONDS
    
    # We build one service at a time to prevent server freezing
    docker compose build "$SERVICE"
    
    DURATION=$(( SECONDS - START_TIME ))
    print_success "$SERVICE build complete (took ${DURATION}s)"
done

# 3. Start all services
print_step "Starting all services in detached mode..."
docker compose up -d
print_success "All services are running!"

# 4. Wait for services to be healthy
print_step "Waiting for services to be ready..."

# Database check
echo -n "Waiting for Database... "
for i in {1..30}; do
    if docker compose exec -T db pg_isready -U user -d esydocs &> /dev/null; then
        print_success "Database ready!"
        break
    fi
    echo -n "."
    sleep 1
done

# API Gateway check
echo -n "Waiting for API Gateway... "
for i in {1..30}; do
    if curl -s http://localhost:8080/healthz &> /dev/null; then
        print_success "API Gateway ready!"
        break
    fi
    echo -n "."
    sleep 1
done

# 5. Summary and Endpoints
print_step "Service Status"
docker compose ps

echo ""
print_success "Deployment successful!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🌐 API Gateway:     http://localhost:8080"
echo "📤 Upload/Job:      http://localhost:8081"
echo "📄 Workers:         8082, 8083, 8084, 8085"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔧 View all logs:   docker compose logs -f"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"