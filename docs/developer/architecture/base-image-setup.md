# Base Image Setup for Faster Builds

There are **two** shared base images. Both can be built explicitly with
`./deployment/build-base-image.sh`; `deploy.sh` also builds either one if it's
missing, and rebuilds `fyredocs-go-builder` on its own whenever the repo's Go
modules have drifted from the copy baked into it:

| Image | Purpose | Rebuild when |
|-------|---------|--------------|
| `fyredocs-base:latest` | **Runtime** base for `convert-to-pdf` — Alpine + LibreOffice/poppler/unoserver | Alpine/LibreOffice/unoserver version changes — **manual** |
| `fyredocs-go-builder:latest` | **Build** base for **every** service — Go toolchain + all module deps + a warm `go-build` cache | `go.work`/any `go.mod`/`go.sum` changes — **automatic** (see [Staleness detection](#staleness-detection)); Go toolchain bump — **manual** |

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
Day-to-day source edits do **not** need a base rebuild — the service build compiles
just the changed packages on top of the warm cache.

The base only goes out of date when its *inputs* change, and those split in two:

| Input | Detected? |
|-------|-----------|
| `go.work`, any `go.mod`/`go.sum` | **Yes** — `deploy.sh` rebuilds automatically |
| `golang:1.25-alpine` toolchain version | **No** — rebuild by hand |

```bash
./deployment/build-base-image.sh        # forces a rebuild of both base images
```

## Staleness detection

The speed of this base comes from snapshotting `go.work` + every `go.mod`/`go.sum` at
build time and pre-populating `/go/pkg/mod` from them. That snapshot is exactly what
makes the image go stale: change a dependency and the image still carries the old
workspace and the old module set.

Left unchecked this surfaces as a confusing failure *deep inside an unrelated service
build*, because a service Dockerfile copies fresh source onto a stale base:

```
go: module ../shared listed in go.work file requires go >= 1.25.7,
    but go.work lists go 1.25.0; to update it: go work use
```

— even though the repo's `go.work` plainly says `1.25.7`. The `1.25.0` came from the
base image's months-old copy.

So the image is **self-describing**:

- `deployment/scripts/gomod-fingerprint.sh` defines `gomod_fingerprint()`, a sha256
  over `go.work`, `go.work.sum`, and every `*/go.mod` + `*/go.sum`.
- `base-go-builder.Dockerfile` stamps it on as `LABEL fyredocs.gomod-hash` (declared
  last, so a changed hash only invalidates the final metadata layer — the expensive
  `go mod download` and warm-compile layers stay cached).
- `ensure_base_images()` in `deploy.sh` recomputes the hash each deploy and rebuilds
  when it differs from the label. A match is a silent no-op, so normal deploys stay
  fast.

Both `deploy.sh` and `build-base-image.sh` source the same helper, so they always
agree on the value.

```bash
# What the current image was built from ("<no value>" = predates the label → stale)
docker image inspect -f '{{index .Config.Labels "fyredocs.gomod-hash"}}' fyredocs-go-builder:latest

# What the repo needs right now
( cd fyredocs_backend && source deployment/scripts/gomod-fingerprint.sh && gomod_fingerprint )
```

The hash covers **only** the Go module blueprint. A Go toolchain or Alpine/LibreOffice
bump is invisible to it — rebuild manually for those.

### Escape hatch
`SKIP_BASE_REBUILD=1 ./deployment/deploy.sh` warns about the stale base but skips the
rebuild. Builds will most likely fail; it exists for the case where you know the drift
is inconsequential (e.g. a `go.sum` touched for a test-only module).

### Defence in depth
Every service Dockerfile also does `COPY go.work go.work.sum* ./` before copying its
source, so the workspace blueprint always matches the source being compiled. That
alone fixes a bumped `go` directive against an older base; a genuinely **new
dependency** still needs the module cache rebuilt, which is what the hash check is for.

## Verify the warm cache
```bash
docker run --rm fyredocs-go-builder:latest ls /root/.cache/go-build   # populated
grep -rln "FROM fyredocs-go-builder" --include=Dockerfile .           # all 11 services
```
