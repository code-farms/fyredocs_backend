# Base Image Setup for Faster Builds

## Problem
LibreOffice installation is slow (~2-5 minutes) and adds ~500MB to the image. Installing it on every build wastes time.

## Solution
Create a pre-built base image with LibreOffice, push it to a container registry, and reuse it.

## One-Time Setup

### Step 1: Build the Base Image

```bash
cd esydocs_backend

# Build the base image
docker build \
  -f docker/base-alpine-libreoffice.Dockerfile \
  -t your-username/alpine-libreoffice:3.19 \
  .
```

### Step 2: Push to Registry

**Option A: Docker Hub (Public)**
```bash
docker login
docker push your-username/alpine-libreoffice:3.19
```

**Option B: GitHub Container Registry (Private/Public)**
```bash
echo $GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin
docker tag your-username/alpine-libreoffice:3.19 ghcr.io/your-username/alpine-libreoffice:3.19
docker push ghcr.io/your-username/alpine-libreoffice:3.19
```

**Option C: Private Registry**
```bash
docker login your-registry.com
docker tag your-username/alpine-libreoffice:3.19 your-registry.com/alpine-libreoffice:3.19
docker push your-registry.com/alpine-libreoffice:3.19
```

### Step 3: Update deploy.sh

Edit `deploy.sh` and update the BASE_IMAGE variable:

```bash
# Near the top of deploy.sh
BASE_IMAGE="your-username/alpine-libreoffice:3.19"

# Then use it in docker build:
docker build \
  --build-arg BASE_IMAGE="${BASE_IMAGE}" \
  -f convert-from-pdf/Dockerfile \
  -t convert-from-pdf:latest \
  .
```

## Using the Base Image

### Fast Build (with base image)
```bash
# After one-time setup above
docker build \
  --build-arg BASE_IMAGE=your-username/alpine-libreoffice:3.19 \
  -f convert-from-pdf/Dockerfile \
  -t convert-from-pdf:latest \
  .
```
**Build time: ~30-60 seconds** (just Go compilation + copy)

### Fallback Build (without base image)
```bash
# Uses default alpine:3.19 and installs everything
docker build \
  -f convert-from-pdf/Dockerfile \
  -t convert-from-pdf:latest \
  .
```
**Build time: ~3-5 minutes** (includes LibreOffice installation)

## Benefits

- **Faster CI/CD**: Base image cached in registry, only download once
- **Faster local builds**: Reuse pre-built layer with LibreOffice
- **Reduced bandwidth**: LibreOffice packages downloaded once, not on every build
- **Consistent environments**: All services use identical LibreOffice version

## Updating the Base Image

Only rebuild when:
- Alpine version changes (3.19 → 3.20)
- LibreOffice needs updating
- Adding new system dependencies

```bash
# Rebuild with new version
docker build -f docker/base-alpine-libreoffice.Dockerfile -t your-username/alpine-libreoffice:3.20 .
docker push your-username/alpine-libreoffice:3.20

# Update Dockerfile ARG default
# In convert-from-pdf/Dockerfile:
# ARG BASE_IMAGE=your-username/alpine-libreoffice:3.20
```

## Cost Considerations

### Docker Hub
- Free: Unlimited public images, 1 private repo
- Pro ($5/mo): Unlimited private repos

### GitHub Container Registry (GHCR)
- Free: Unlimited public images
- Free: 500MB private storage (more than enough for this base image ~500MB)

### Self-Hosted Registry
- Free: Run your own registry (requires server)
- Example: Harbor, Docker Registry

## Quick Start Script

Use the provided script:

```bash
# Edit docker/build-base-image.sh with your registry details
nano docker/build-base-image.sh

# Run the script
./docker/build-base-image.sh
```
