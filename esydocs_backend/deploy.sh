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

# Check if openssl is available for generating secrets
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
    print_warning "IMPORTANT: Keep this file secure and don't commit it to git!"
fi

# Validate JWT secret
if [ ${#JWT_HS256_SECRET} -lt 32 ]; then
    print_error "JWT secret is too short (must be at least 32 characters)"
    exit 1
fi

print_success "JWT secret validated (${#JWT_HS256_SECRET} characters)"

# Note: PDF processing now uses free open-source tools
# No API keys needed! (pdfcpu, LibreOffice, Poppler)
print_step "PDF Processing Configuration"
print_success "Using free open-source tools (pdfcpu, LibreOffice, Poppler)"
print_success "No API keys or subscriptions required!"

# Stop existing containers
print_step "Stopping existing containers..."
docker compose down --remove-orphans 2>/dev/null || true
print_success "Stopped existing containers"

# Build and start all services
print_step "Building and starting all services..."
docker compose up -d --build

# Wait for services to be healthy
print_step "Waiting for services to be ready..."

print_step "Starting PostgreSQL Database..."
echo -n "Waiting for database... "
for i in {1..30}; do
    if docker compose exec -T db pg_isready -U user -d esydocs &> /dev/null; then
        print_success "Database is ready!"
        break
    fi
    echo -n "."
    sleep 1
done

print_step "Starting Redis Cache..."
echo -n "Waiting for Redis... "
for i in {1..10}; do
    if docker compose exec -T redis redis-cli ping &> /dev/null; then
        print_success "Redis is ready!"
        break
    fi
    echo -n "."
    sleep 1
done

print_step "Starting API Gateway (port 8080)..."
echo -n "Waiting for API Gateway... "
for i in {1..30}; do
    if curl -s http://localhost:8080/healthz &> /dev/null; then
        print_success "API Gateway is ready!"
        break
    fi
    echo -n "."
    sleep 1
done

print_step "Starting Upload Service (port 8081)..."
echo -n "Waiting for Upload Service... "
for i in {1..30}; do
    if curl -s http://localhost:8081/healthz &> /dev/null; then
        print_success "Upload Service is ready!"
        break
    fi
    echo -n "."
    sleep 1
done

print_step "Starting Convert-From-PDF Service (port 8082)..."
echo -n "Waiting for Convert-From-PDF Service... "
for i in {1..30}; do
    if curl -s http://localhost:8082/healthz &> /dev/null; then
        print_success "Convert-From-PDF Service is ready!"
        break
    fi
    echo -n "."
    sleep 1
done

print_step "Starting Convert-To-PDF Service (port 8083)..."
echo -n "Waiting for Convert-To-PDF Service... "
for i in {1..30}; do
    if curl -s http://localhost:8083/healthz &> /dev/null; then
        print_success "Convert-To-PDF Service is ready!"
        break
    fi
    echo -n "."
    sleep 1
done

print_step "Starting Organize-PDF Service (port 8084)..."
echo -n "Waiting for Organize-PDF Service... "
for i in {1..30}; do
    if curl -s http://localhost:8084/healthz &> /dev/null; then
        print_success "Organize-PDF Service is ready!"
        break
    fi
    echo -n "."
    sleep 1
done

# Show service status
print_step "Service Status"
docker compose ps

# Show logs command
echo ""
print_success "All services started successfully!"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "📋 Service Endpoints:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  🌐 API Gateway:        http://localhost:8080"
echo "  📤 Upload Service:     http://localhost:8081"
echo "  📄 Convert-From-PDF:   http://localhost:8082"
echo "  📑 Convert-To-PDF:     http://localhost:8083"
echo "  📋 Organize-PDF:       http://localhost:8084"
echo "  🗄️ PostgreSQL:         localhost:5432"
echo "  🔴 Redis:              localhost:6379"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔧 Useful Commands:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  View logs:             docker compose logs -f"
echo "  View specific service: docker compose logs -f api-gateway"
echo "  Restart services:      docker compose restart"
echo "  Stop all:              docker compose down"
echo "  Remove all data:       docker compose down -v"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔐 Security Info:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  JWT Secret stored in:  $JWT_SECRET_FILE"
echo "  PDF Processing:        Free Open-Source Tools ✓"
echo "                         (pdfcpu, LibreOffice, Poppler)"
echo "  ⚠️  Keep JWT secret file secure and never commit it!"
echo "  ⚠️  For production, use proper secret management"
echo ""
