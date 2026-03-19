#!/usr/bin/env bash
set -euo pipefail

# Start the Global Timer
GLOBAL_START_TIME=$SECONDS

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

# Check requirements
if ! command -v openssl &> /dev/null; then
    print_error "openssl is required but not installed."
    exit 1
fi

# Load environment variables from .env file (optional — server env vars also accepted)
ENV_FILE=".env"
if [ -f "$ENV_FILE" ]; then
    print_success "Loading environment from $ENV_FILE"
    set -a
    source "$ENV_FILE"
    set +a
else
    print_warning "No .env file found — using server environment variables"
fi

# Validate required environment variables
if [ -z "${POSTGRES_USER:-}" ] || [ -z "${POSTGRES_PASSWORD:-}" ]; then
    print_error "POSTGRES_USER and POSTGRES_PASSWORD must be set (in .env or server environment)"
    exit 1
fi

if [ -z "${REDIS_PASSWORD:-}" ]; then
    print_error "REDIS_PASSWORD must be set (in .env or server environment)"
    exit 1
fi

# Generate or load JWT secret
JWT_SECRET_FILE=".jwt_secret"
if [ -f "$JWT_SECRET_FILE" ]; then
    print_warning "Found existing JWT secret in $JWT_SECRET_FILE"
    export JWT_HS256_SECRET=$(cat "$JWT_SECRET_FILE")
else
    print_step "Generating new JWT secret..."
    export JWT_HS256_SECRET=$(openssl rand -hex 32)
    echo "$JWT_HS256_SECRET" > "$JWT_SECRET_FILE"
    chmod 600 "$JWT_SECRET_FILE"
    print_success "Generated new JWT secret"
fi

# Configuration Notice
print_step "PDF Processing Configuration"
print_success "Using free open-source tools (pdfcpu, LibreOffice, Poppler)"
print_success "Using Go Workspace caching for ultra-fast builds"

# 1. Stop existing containers
print_step "Stopping existing containers..."
docker compose down --remove-orphans 2>/dev/null || true
print_success "Stopped existing containers"

# 2. Sequential Build Stage (CPU-Safe Mode)
print_step "Building Go Services sequentially..."

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

export DOCKER_BUILDKIT=1

for SERVICE in "${GO_SERVICES[@]}"; do
    echo -e "${YELLOW}🔨 Starting build for: $SERVICE...${NC}"
    STEP_START=$SECONDS
    
    docker compose build "$SERVICE"
    
    STEP_DURATION=$(( SECONDS - STEP_START ))
    print_success "$SERVICE build complete (took ${STEP_DURATION}s)"
done

# 3. Start all services
print_step "Starting all services in detached mode..."
docker compose up -d
print_success "All services are running!"

# 4. Wait for services to be healthy
print_step "Waiting for services to be ready..."

echo -n "Waiting for Database... "
for i in {1..30}; do
    if docker compose exec -T db pg_isready -U "$POSTGRES_USER" -d "${POSTGRES_DB:-esydocs}" &> /dev/null; then
        print_success "Database ready!"
        break
    fi
    if [ $i -eq 30 ]; then
        print_error "Database failed to start within 30s!"
        docker compose logs db | tail -20
        exit 1
    fi
    echo -n "."
    sleep 1
done

echo -n "Waiting for API Gateway... "
for i in {1..30}; do
    if curl -s http://localhost:8080/healthz &> /dev/null; then
        print_success "API Gateway ready!"
        break
    fi
    if [ $i -eq 30 ]; then
        print_error "API Gateway failed to start within 30s!"
        docker compose logs api-gateway | tail -20
        exit 1
    fi
    echo -n "."
    sleep 1
done

# 5. Post-Deployment Cleanup
print_step "Optimizing Disk Space"
docker image prune -f
print_success "Removed unused image layers"

# 6. Final Summary and Endpoints
GLOBAL_DURATION=$(( SECONDS - GLOBAL_START_TIME ))
MINUTES=$(( GLOBAL_DURATION / 60 ))
SECONDS_REM=$(( GLOBAL_DURATION % 60 ))

print_step "Service Status"
docker compose ps

echo ""
print_success "Deployment successful! (Total Time: ${MINUTES}m ${SECONDS_REM}s)"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "📋 Service Endpoints:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  🌐 API Gateway:        http://localhost:8080"
echo "  📤 Upload Service:     http://localhost:8081"
echo "  📄 Convert-From-PDF:   http://localhost:8082"
echo "  📑 Convert-To-PDF:     http://localhost:8083"
echo "  📋 Organize-PDF:       http://localhost:8084"
echo "  🔧 Optimize-PDF:       http://localhost:8085"
echo "  🗄️  PostgreSQL:         localhost:5432"
echo "  🔴 Redis:              localhost:6379"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔧 Useful Commands:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  View logs:             docker compose logs -f"
echo "  View specific service: docker compose logs -f api-gateway"
echo "  Restart services:      docker compose restart"
echo "  Stop all:              docker compose down"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔐 Security Info:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  JWT Secret stored in:  $JWT_SECRET_FILE"
echo "  ⏱️  Total Deploy Time:  ${MINUTES}m ${SECONDS_REM}s"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""