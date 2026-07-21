# Base Image Setup for Faster Builds

There are **two** shared base images. Both are built once by
`./deployment/build-base-image.sh` (and auto-built by `deploy.sh` if missing):

| Image | Purpose | Rebuild when |
|-------|---------|--------------|
| `fyredocs-base:latest` | **Runtime** base for `convert-to-pdf` — Alpine + LibreOffice/poppler/unoserver | Alpine/LibreOffice/unoserver version changes |
| `fyredocs-go-builder:latest` | **Build** base for **every** service — Go toolchain + all module deps + a warm `go-build` cache | any service `go.mod`/`go.sum`, or the Go toolchain version, changes |

The Go builder base is documented in its own section below ("Go Builder Base Image").

## Problem
LibreOffice installation is slow (~2-5 minutes) and adds ~500MB to the image. Installing it on every build wastes time.

## Solution
Create a pre-built base image with LibreOffice, push it to a container registry, and reuse it.

## One-Time Setup

### Step 1: Build the Base Image

```bash
cd fyredocs_backend

# Build the base image
docker build \
  -f deployment/base-alpine-libreoffice.Dockerfile \
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
docker build -f deployment/base-alpine-libreoffice.Dockerfile -t your-username/alpine-libreoffice:3.20 .
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
# Edit deployment/build-base-image.sh with your registry details
nano deployment/build-base-image.sh

# Run the script
./deployment/build-base-image.sh
```

---

# Go Builder Base Image (`fyredocs-go-builder`)

## Problem
Each service Dockerfile used to repeat the same ~18-line blueprint (copy `go.work` +
all 11 `go.mod` + `go mod download`) and then compile against a **BuildKit
`type=cache` mount** for `/root/.cache/go-build`. That mount lives only in the local
builder and is **not** part of any image, so on a fresh builder or after
`docker builder prune` it is empty. When a single service's source changes and the
cache is cold, that one `go build` recompiles the entire dependency graph
(`quic-go`, `otel`/`otlp`, `grpc`, `gorm` ×3 drivers, `gin`, `pdfcpu`, …) + stdlib
from scratch — observed at **~21 minutes** for `optimize-pdf`, and it can hit any
service whose source changed.

## Solution
`deployment/base-go-builder.Dockerfile` builds `fyredocs-go-builder:latest`, which
bakes into its **image layers** (not a cache mount):

- the full module cache (`/go/pkg/mod`),
- a **warm** Go build cache (`/root/.cache/go-build`) for the whole dependency graph,
  compiled once with the same flags services use (`CGO_ENABLED=0 GOOS=linux -trimpath`),
- the workspace blueprint (`go.work` + every `go.mod`).

Because the cache is in the image, it survives `docker builder prune` and fresh
machines. Every service Dockerfile does `FROM fyredocs-go-builder:latest AS builder`,
copies its own fresh source, and recompiles only its own packages → a cold ~21-min
compile becomes tens of seconds.

## Build parallelism
Both the base warm-compile and the per-service builds run
`go build -p ${GO_BUILD_PARALLELISM}` (default **6** — leaves 2 of an 8-core box free
for the daemon/OS). Set once in `deploy.sh`; override per-machine:

```bash
GO_BUILD_PARALLELISM=4 ./deployment/deploy.sh
GO_BUILD_PARALLELISM=4 ./deployment/build-base-image.sh
```

## When to rebuild
Rebuild `fyredocs-go-builder` **only when deps or the toolchain change** (any service
`go.mod`/`go.sum`, or the `golang:1.25-alpine` version). Day-to-day source edits do
**not** need a base rebuild — the service build downloads/compiles just the changed
packages on top of the warm cache.

```bash
./deployment/build-base-image.sh        # rebuilds both base images
```

`deploy.sh` builds the base automatically only if it's missing (via
`ensure_base_images`), so a fresh machine's first deploy pays the one-time warm cost
and subsequent deploys are fast.

## Verify the warm cache
```bash
docker run --rm fyredocs-go-builder:latest ls /root/.cache/go-build   # populated
grep -rln "FROM fyredocs-go-builder" --include=Dockerfile .           # all 11 services
```
