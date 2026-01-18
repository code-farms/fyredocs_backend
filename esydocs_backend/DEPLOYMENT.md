# EsyDocs Backend Deployment Guide

## Quick Start (Local Development)

### Prerequisites
- Docker and Docker Compose installed
- OpenSSL installed (for generating JWT secrets)
- Git Bash or WSL (on Windows)

### One-Command Deploy

```bash
./deploy.sh
```

That's it! The script will:
1. ✅ Generate a secure JWT secret (or reuse existing one)
2. ✅ Stop any existing containers
3. ✅ Build all Docker images
4. ✅ Start all services with proper environment variables
5. ✅ Wait for services to be healthy
6. ✅ Display service endpoints and useful commands

### Services Started

| Service | Port | URL |
|---------|------|-----|
| API Gateway | 8080 | http://localhost:8080 |
| Upload Service | 8081 | http://localhost:8081 |
| PostgreSQL | 5432 | localhost:5432 |
| Redis | 6379 | localhost:6379 |
| Convert From PDF | - | (internal) |
| Convert To PDF | - | (internal) |
| Cleanup Worker | - | (background) |

---

## Environment Variables

All environment variables are managed in the `docker-compose.yml` file. The deploy script automatically generates and injects the JWT secret.

### Security Configuration

The following P0 security fixes are **enabled by default**:

| Setting | Value | Description |
|---------|-------|-------------|
| `JWT_HS256_SECRET` | Auto-generated | 256-bit cryptographically random secret |
| `AUTH_DENYLIST_ENABLED` | `true` | Logout revokes access tokens immediately |
| `AUTH_COOKIE_SECURE` | `false` (local) | Set to `true` in production with HTTPS |
| `RATE_LIMIT_LOGIN` | `5/min` | Prevents brute force attacks |
| `RATE_LIMIT_SIGNUP` | `3/min` | Prevents account creation spam |
| `RATE_LIMIT_REFRESH` | `10/min` | Prevents token exhaustion |

---

## Common Commands

### View Logs
```bash
# All services
docker compose logs -f

# Specific service
docker compose logs -f api-gateway
docker compose logs -f upload-service

# Last 100 lines
docker compose logs --tail=100 -f
```

### Restart Services
```bash
# Restart all
docker compose restart

# Restart specific service
docker compose restart api-gateway
```

### Stop All Services
```bash
docker compose down
```

### Stop and Remove All Data (⚠️ DESTRUCTIVE)
```bash
docker compose down -v
```

### Rebuild Specific Service
```bash
docker compose up -d --build upload-service
```

### Check Service Status
```bash
docker compose ps
```

### Execute Commands Inside Containers
```bash
# Access database
docker compose exec db psql -U user -d esydocs

# Access Redis
docker compose exec redis redis-cli

# Access service shell
docker compose exec upload-service sh
```

---

## Manual Deployment (Without Script)

If you prefer to deploy manually:

### 1. Generate JWT Secret
```bash
# Generate and export
export JWT_HS256_SECRET=$(openssl rand -hex 32)

# Or set it manually
export JWT_HS256_SECRET="your-64-character-hex-secret-here"
```

### 2. Start Services
```bash
docker compose up -d --build
```

### 3. Check Health
```bash
# Database
docker compose exec db pg_isready -U user -d esydocs

# Redis
docker compose exec redis redis-cli ping

# API Gateway
curl http://localhost:8080/healthz

# Upload Service
curl http://localhost:8081/healthz
```

---

## Production Deployment

### Security Checklist

Before deploying to production:

- [ ] **Generate a new JWT secret** (don't reuse dev secret)
- [ ] **Enable HTTPS** and set `AUTH_COOKIE_SECURE=true`
- [ ] **Use proper secret management** (AWS Secrets Manager, Azure Key Vault, etc.)
- [ ] **Update database credentials** (don't use default user/password)
- [ ] **Configure CORS origins** to match your frontend domain
- [ ] **Set up Redis persistence** or use managed Redis
- [ ] **Configure backup strategy** for PostgreSQL
- [ ] **Enable monitoring and logging**
- [ ] **Set up SSL/TLS certificates**
- [ ] **Review and adjust rate limits** based on traffic

### Environment Variables for Production

Override these in production:

```yaml
services:
  api-gateway:
    environment:
      JWT_HS256_SECRET: ${JWT_HS256_SECRET}  # From secret manager
      CORS_ALLOW_ORIGINS: "https://yourdomain.com"
      # ... other vars

  upload-service:
    environment:
      JWT_HS256_SECRET: ${JWT_HS256_SECRET}  # Same as api-gateway
      AUTH_COOKIE_SECURE: "true"  # MUST be true in production
      DATABASE_URL: ${DATABASE_URL}  # From secret manager
      # ... other vars
```

### Production Deployment Options

#### Option 1: Docker Compose on VPS
```bash
# On your server
export JWT_HS256_SECRET="your-production-secret"
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

#### Option 2: Kubernetes
See the deployment plan in the main README for Kubernetes configuration.

#### Option 3: Cloud Platforms
- **AWS ECS/Fargate**: Use task definitions with secrets from Secrets Manager
- **Azure Container Instances**: Use secure environment variables
- **Google Cloud Run**: Use Secret Manager integration

---

## Troubleshooting

### JWT Secret Validation Fails

**Error**: `JWT secret validation failed: JWT_HS256_SECRET environment variable is required`

**Solution**: Ensure the JWT secret is generated:
```bash
./deploy.sh  # This will auto-generate it
```

Or manually:
```bash
export JWT_HS256_SECRET=$(openssl rand -hex 32)
docker compose up -d
```

### Services Won't Start

**Check logs**:
```bash
docker compose logs api-gateway
docker compose logs upload-service
```

**Common issues**:
- Port already in use: Change ports in `docker-compose.yml`
- Database not ready: Wait for healthy status
- Redis connection failed: Ensure Redis is running

### Database Connection Issues

**Reset database**:
```bash
docker compose down -v  # ⚠️ Deletes all data
docker compose up -d
```

**Access database directly**:
```bash
docker compose exec db psql -U user -d esydocs
```

### Rate Limiting Too Strict

**Temporarily disable**:
Edit `docker-compose.yml`:
```yaml
RATE_LIMIT_LOGIN: "100"  # Increase limits
RATE_LIMIT_SIGNUP: "100"
RATE_LIMIT_REFRESH: "100"
```

Then:
```bash
docker compose up -d
```

---

## Security Notes

### JWT Secret Management

The deploy script stores the JWT secret in `.jwt_secret` file:
- ✅ Automatically gitignored
- ✅ Persists across deployments
- ✅ 600 permissions (owner read/write only)
- ⚠️ For production, use proper secret management

### Default Credentials

**⚠️ CHANGE THESE IN PRODUCTION:**
- PostgreSQL: `user` / `password`
- Database name: `esydocs`

### HTTPS in Production

The default configuration uses `AUTH_COOKIE_SECURE=false` for local development.

**For production**, you MUST:
1. Set up HTTPS (use nginx/traefik/caddy as reverse proxy)
2. Set `AUTH_COOKIE_SECURE=true` in docker-compose.yml
3. Update CORS origins to HTTPS URLs

---

## File Structure

```
esydocs_backend/
├── docker-compose.yml          # Main compose file (all services)
├── deploy.sh                   # One-command deployment script
├── .jwt_secret                 # Generated JWT secret (gitignored)
├── DEPLOYMENT.md              # This file
│
├── api-gateway/
│   ├── Dockerfile
│   └── .env.example           # Template with secure defaults
│
├── upload-service/
│   ├── Dockerfile
│   └── .env.example           # Template with secure defaults
│
└── ... (other services)
```

---

## Need Help?

- Check service logs: `docker compose logs -f [service-name]`
- View service status: `docker compose ps`
- Restart services: `docker compose restart`
- For security issues, review the security analysis report

---

## Next Steps

After deployment:
1. Test the authentication flow (signup, login, logout)
2. Verify rate limiting works (try 6+ login attempts)
3. Check that logout revokes tokens (test access after logout)
4. Review logs for any warnings or errors
5. Set up monitoring and alerting for production
