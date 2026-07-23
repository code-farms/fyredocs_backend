#!/usr/bin/env bash
set -euo pipefail

# Start the Global Timer
GLOBAL_START_TIME=$SECONDS

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(cd "$SCRIPT_DIR/.." && pwd)
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

# Go build parallelism: how many parallel compile actions `go build` may run.
# Capped below the core count so the Docker daemon/OS keep headroom during builds
# (8-core box → leave 2). Override per-machine: GO_BUILD_PARALLELISM=4 deployment/deploy.sh
GO_BUILD_PARALLELISM="${GO_BUILD_PARALLELISM:-6}"

# Build the shared base images that every service FROM-s — only if they're missing.
# These are "build once" images: rebuild them explicitly with
# ./deployment/build-base-image.sh whenever go.mod deps, the Go toolchain, or the
# LibreOffice/Alpine versions change. fyredocs-go-builder bakes the Go module cache
# AND a warm go-build cache into image layers, so each service recompiles only its
# own packages (turns a cold ~20-min compile into tens of seconds) and the cache
# survives `docker builder prune` / fresh machines.
ensure_base_images() {
    if ! docker image inspect fyredocs-base:latest >/dev/null 2>&1; then
        print_step "Building fyredocs-base image (LibreOffice/poppler)..."
        docker build -f deployment/base-alpine-libreoffice.Dockerfile -t fyredocs-base:latest .
        print_success "fyredocs-base image built"
    fi
    if ! docker image inspect fyredocs-go-builder:latest >/dev/null 2>&1; then
        print_step "Building fyredocs-go-builder image (Go deps + warm build cache)..."
        docker build -f deployment/base-go-builder.Dockerfile \
            --build-arg GO_BUILD_PARALLELISM="$GO_BUILD_PARALLELISM" \
            -t fyredocs-go-builder:latest .
        print_success "fyredocs-go-builder image built"
    fi
}

usage() {
    cat <<'USAGE'
Usage: deployment/deploy.sh [options] [service ...]

  (no args)              Full deploy: stop stack, build all services
                         sequentially, start everything, wait for the edge.
  <service> [more...]    Rebuild + redeploy only the named service(s) via
                         deployment/docker-compose-<service>.yml. The rest of
                         the running stack is untouched.
  --dry-run, --print-budget
                         Show the resource budget for this host and exit.
  --rollback=<sha>       Roll the whole stack back to a previously-built image
                         tag (fyredocs-<svc>:<sha>) without rebuilding. List
                         available tags with:  docker images 'fyredocs-*'
  -h, --help             Show this help.

See docs/developer/architecture/compose-files.md for the compose files layout.
Every full deploy tags each service image as fyredocs-<svc>:<git-sha> and keeps
the most recent IMAGE_TAG_RETAIN (default 5) so you can roll back.
USAGE
}

# --print-budget / --dry-run: compute and show the resource budget for this
# host (RESOURCE_BUDGET_PCT%, default 70), then exit without building anything.
# Bare arguments are service names for a single-service deploy.
DRY_RUN=0
ROLLBACK_SHA=""
SERVICES_TO_DEPLOY=()
for arg in "$@"; do
    case "$arg" in
        --dry-run|--print-budget) DRY_RUN=1 ;;
        --rollback=*) ROLLBACK_SHA="${arg#*=}" ;;
        -h|--help) usage; exit 0 ;;
        -*) print_error "Unknown option: $arg"; usage; exit 1 ;;
        *) SERVICES_TO_DEPLOY+=("$arg") ;;
    esac
done

# The Docker Compose project name (compose file `name:`) — built service images
# are auto-tagged <project>-<service>:latest. We add :<git-sha> tags for rollback.
COMPOSE_PROJECT="fyredocs"
GO_SERVICES=(
  "api-gateway" "analytics-service" "auth-service" "job-service"
  "convert-from-pdf" "convert-to-pdf" "organize-pdf" "optimize-pdf"
  "document-service" "user-service" "notification-service"
)
# Short git SHA of the tree being deployed (falls back to a timestamp-free label
# when not in a git checkout — Date.now-style values would break reproducibility).
GIT_SHA="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo "untagged")"
IMAGE_TAG_RETAIN="${IMAGE_TAG_RETAIN:-5}"

# tag_built_images tags each freshly built service image as fyredocs-<svc>:<sha>
# so a specific build can be rolled back to. The moving :latest tag keeps
# pointing at the newest build.
tag_built_images() {
    [ "$GIT_SHA" = "untagged" ] && { print_warning "Not a git checkout — skipping image SHA tagging"; return 0; }
    for svc in "${GO_SERVICES[@]}"; do
        local id
        id="$(docker compose images -q "$svc" 2>/dev/null || true)"
        [ -n "$id" ] || continue
        docker tag "$id" "${COMPOSE_PROJECT}-${svc}:${GIT_SHA}" 2>/dev/null || true
    done
    print_success "Tagged service images @ ${GIT_SHA}"
}

# prune_old_image_tags keeps only the newest IMAGE_TAG_RETAIN SHA tags per
# service so retained rollback images don't grow without bound.
prune_old_image_tags() {
    for svc in "${GO_SERVICES[@]}"; do
        # Newest-first by created time; skip :latest; drop everything past the keep count.
        docker images "${COMPOSE_PROJECT}-${svc}" --format '{{.Tag}} {{.CreatedAt}}' 2>/dev/null \
            | grep -v '^latest ' \
            | sort -rk2 \
            | awk 'NR>'"$IMAGE_TAG_RETAIN"' {print $1}' \
            | while read -r tag; do
                [ -n "$tag" ] && docker rmi "${COMPOSE_PROJECT}-${svc}:${tag}" 2>/dev/null || true
              done
    done
}

# do_rollback retags a previously-built :<sha> image back onto :latest for every
# service and recreates the stack from those images WITHOUT rebuilding.
do_rollback() {
    local sha="$1"
    print_step "Rolling back to image tag: ${sha}"
    local missing=0
    for svc in "${GO_SERVICES[@]}"; do
        if ! docker image inspect "${COMPOSE_PROJECT}-${svc}:${sha}" >/dev/null 2>&1; then
            print_error "Missing image ${COMPOSE_PROJECT}-${svc}:${sha}"
            missing=1
        fi
    done
    if [ "$missing" -ne 0 ]; then
        print_error "Rollback aborted — not all services have a ${sha} image. Available:"
        docker images "${COMPOSE_PROJECT}-*" | grep -v ':latest' || true
        exit 1
    fi
    for svc in "${GO_SERVICES[@]}"; do
        docker tag "${COMPOSE_PROJECT}-${svc}:${sha}" "${COMPOSE_PROJECT}-${svc}:latest"
    done
    export DOCKER_BUILDKIT=1
    export COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
    export COMPOSE_ENV_FILES="$ROOT_DIR/.env"
    export COMPOSE_PROFILES="${COMPOSE_PROFILES:-observability}"
    docker compose up -d --force-recreate --no-build "${GO_SERVICES[@]}"
    print_success "Rolled back to ${sha}"
}

# ---------------------------------------------------------------------------
# compute_resource_budget
#
# Caps the WHOLE stack at RESOURCE_BUDGET_PCT% (default 70) of the machine's
# total available RAM and CPU,
# on any host, without hardcoding specs. "Total available" is read from
# `docker info` (.MemTotal / .NCPU): on a Linux VPS that is the full host; on
# macOS Docker Desktop it is the VM's allocation (raise the VM and this picks
# it up next run). Override with MEM_TOTAL_MB / NCPU env vars (used for the
# --dry-run "what would a bigger box get?" check).
#
# The budget is distributed across the 16 weighted containers by
# responsibility-based weights (see the MEMW/CPUW tables below) — this includes
# the co-located Postgres (db) and the db-backup sidecar; only caddy is excluded
# (it belongs to the reserved OS/edge headroom). The absolute numbers scale to the
# budget on any host. Because a container can never exceed its own cgroup limit,
# Σ(limits) ≤ budget guarantees the whole project stays within the cap.
#
# Worker pools (concurrency / OCR workers / unoserver instances) are then
# derived from each worker's SCALED memory cap, so a pool can never be sized
# past the RAM its own container is allowed — no per-container OOM-kill on any
# host, and the unoserver instance count is pinned (entrypoint and Go pool
# agree, fixing the port/daemon mismatch).
# ---------------------------------------------------------------------------
compute_resource_budget() {
    # Parallel indexed arrays (bash 3.2 safe — macOS system bash has no
    # associative arrays). Order: name, mem weight (MB), cpu weight (tenths).
    local NAMES=(redis nats minio api-gateway auth-service job-service \
        convert-from-pdf convert-to-pdf organize-pdf optimize-pdf \
        analytics-service document-service user-service notification-service \
        db db-backup)
    # Weights reflect each service's real responsibilities (not the old 8 GB-box
    # limits). Memory: LibreOffice/OCR workers + MinIO get the bulk; redis and
    # the light Go CRUD services stay lean. CPU: the heavy workers AND the
    # api-gateway — which sits on every request's hot path and proxies object
    # bytes to/from MinIO — get real shares; near-idle services get a sliver.
    # Sums are computed below so the arrays can be retuned in isolation.
    # job-service's weights include its in-process cleanup sweep (formerly
    # the separate cleanup-worker container).
    # db = the co-located Postgres backing the whole stack: the largest non-worker
    # memory weight (lands ~1G on a 16 GB box) and a gateway-sized CPU share (it is
    # app/IO-bound, not DB-CPU-bound). db-backup = a near-idle pg_dump|gzip|rclone
    # sidecar. Both were previously hardcoded OUTSIDE this budget; folding them in
    # is what makes Σ(limits) ≤ budget actually hold for the full stack. (caddy is
    # deliberately excluded — it belongs to the reserved OS/edge headroom.)
    #              redis nats minio  gw auth  job   cF  cT org opt  an doc usr ntf   db db-bk
    local MEMW=(     8   12   34   14    7   24   48  48  24  46    6   6   6   6   26    3)
    local CPUW=(     2    2    7   10    2    6   18  18  12  18    1   1   1   1   10    1)
    local MEMW_SUM=0 CPUW_SUM=0 wi
    for wi in "${!MEMW[@]}"; do
        MEMW_SUM=$(( MEMW_SUM + MEMW[wi] )); CPUW_SUM=$(( CPUW_SUM + CPUW[wi] ))
    done
    # heavy-worker mem (MB) + cpu (hundredths) captured in the loop for pools
    local MEM_TO CH_TO MEM_CF CH_CF MEM_ORG CH_ORG MEM_OPT CH_OPT MEM_REDIS

    # --- total available (docker info, overridable) ---
    if [ -z "${MEM_TOTAL_MB:-}" ]; then
        local mem_bytes
        mem_bytes=$(docker info --format '{{.MemTotal}}' 2>/dev/null || echo 0)
        MEM_TOTAL_MB=$(( mem_bytes / 1048576 ))
    fi
    if [ -z "${NCPU:-}" ]; then
        NCPU=$(docker info --format '{{.NCPU}}' 2>/dev/null || echo 0)
    fi

    if [ "${MEM_TOTAL_MB:-0}" -le 0 ] || [ "${NCPU:-0}" -le 0 ]; then
        print_warning "Could not read host RAM/CPU from 'docker info' (is Docker running?)."
        print_warning "Skipping 70% auto-budget — compose built-in defaults apply."
        return 0
    fi

    # --- budget percentage (configurable; default 70, clamped to a sane range) ---
    # Lower = more headroom for the OS/page-cache/burst; higher = more throughput
    # on a dedicated box. >90 risks the host OOM-killer on spiky LibreOffice/OCR
    # bursts; <50 wastes the box.
    local PCT="${RESOURCE_BUDGET_PCT:-70}"
    case "$PCT" in *[!0-9]*|"") PCT=70 ;; esac    # non-numeric → default
    [ "$PCT" -gt 90 ] && PCT=90
    [ "$PCT" -lt 50 ] && PCT=50

    local MEM_BUDGET=$(( MEM_TOTAL_MB * PCT / 100 ))  # MB, floor
    local CPU_BUDGET_H=$(( NCPU * PCT ))              # (PCT/100) * NCPU, in hundredths

    print_step "Resource Budget (${PCT}% cap)"
    printf "  Host available (docker info): %s MB RAM, %s CPU\n" "$MEM_TOTAL_MB" "$NCPU"
    printf "  %d%% project budget:           %s MB RAM, %d.%02d CPU\n" \
        "$PCT" "$MEM_BUDGET" "$((CPU_BUDGET_H/100))" "$((CPU_BUDGET_H%100))"
    printf "  %-22s %10s %8s\n" "SERVICE" "MEM" "CPU"
    printf "  %-22s %10s %8s\n" "----------------------" "----------" "--------"

    local mem_sum=0 cpu_sum_h=0
    local i name var mem cpu_h res
    for i in "${!NAMES[@]}"; do
        name="${NAMES[$i]}"
        mem=$(( MEM_BUDGET * MEMW[i] / MEMW_SUM ))            # MB, floor
        cpu_h=$(( CPU_BUDGET_H * CPUW[i] / CPUW_SUM ))        # hundredths of a core, floor
        [ "$cpu_h" -lt 1 ] && cpu_h=1                         # never advertise 0.00 cpus
        var=$(echo "$name" | tr 'a-z-' 'A-Z_')
        export "${var}_MEM_LIMIT=${mem}m"
        export "${var}_CPU_LIMIT=$(printf '%d.%02d' $((cpu_h/100)) $((cpu_h%100)))"
        # reservation = 50% of the scaled limit, so it is ALWAYS <= the limit
        # (Docker rejects reservation > limit). Floor at 6 MB.
        res=$(( mem * 50 / 100 )); [ "$res" -lt 6 ] && res=6
        export "${var}_MEM_RESERVATION=${res}m"
        mem_sum=$(( mem_sum + mem ))
        cpu_sum_h=$(( cpu_sum_h + cpu_h ))
        printf "  %-22s %9sm %5d.%02d\n" "$name" "$mem" "$((cpu_h/100))" "$((cpu_h%100))"
        # stash heavy-worker mem/cpu for pool derivation below
        case "$name" in
            convert-to-pdf)   MEM_TO=$mem;  CH_TO=$cpu_h ;;
            convert-from-pdf) MEM_CF=$mem;  CH_CF=$cpu_h ;;
            organize-pdf)     MEM_ORG=$mem; CH_ORG=$cpu_h ;;
            optimize-pdf)     MEM_OPT=$mem; CH_OPT=$cpu_h ;;
            redis)            MEM_REDIS=$mem ;;
        esac
    done
    printf "  %-22s %9sm %5d.%02d   (Σ ≤ budget)\n" "TOTAL" "$mem_sum" \
        "$((cpu_sum_h/100))" "$((cpu_sum_h%100))"

    # --- pools derived from each worker's SCALED caps ---
    # Pool COUNT is bounded by memory (so it can't OOM its own container) and by
    # a CPU "slots" allowance of ~2 per allocated core (workers are partly
    # IO-bound; the cgroup cpu limit throttles real usage regardless). min 1.
    local U_TO=$((  CH_TO  * 2 / 100 )); [ "$U_TO"  -lt 1 ] && U_TO=1
    local U_CF=$((  CH_CF  * 2 / 100 )); [ "$U_CF"  -lt 1 ] && U_CF=1
    local U_ORG=$(( CH_ORG * 2 / 100 )); [ "$U_ORG" -lt 1 ] && U_ORG=1
    local U_OPT=$(( CH_OPT * 2 / 100 )); [ "$U_OPT" -lt 1 ] && U_OPT=1

    # Each pool: auto-size from the scaled caps, but honor a value the operator
    # pinned in .env (explicit opt-out wins). The mem/cpu LIMITS above stay
    # always-auto so the 70% cap can never be accidentally removed.
    # convert-to-pdf: ~300 MB per unoserver daemon, ~200 MB Go/buffer overhead.
    local insts=$(( (MEM_TO - 200) / 300 )); [ "$insts" -lt 1 ] && insts=1
    [ "$insts" -gt "$U_TO" ] && insts=$U_TO
    [ "$insts" -gt 6 ] && insts=6
    [ -n "${UNOSERVER_INSTANCES:-}" ] && insts="$UNOSERVER_INSTANCES"
    export UNOSERVER_INSTANCES="$insts"
    local cto=$(( insts - 1 )); [ "$cto" -lt 1 ] && cto=1
    [ -n "${CONVERT_TO_PDF_CONCURRENCY:-}" ] && cto="$CONVERT_TO_PDF_CONCURRENCY"
    export CONVERT_TO_PDF_CONCURRENCY="$cto"

    # optimize-pdf: peak tesseract processes ≈ concurrency × ocr_workers, each
    # ~250 MB. Size both from one slot budget (~100 MB Go overhead) so the
    # product fits the container cap. OCR page-pool capped at U_OPT and 6.
    local opt_slots=$(( (MEM_OPT - 100) / 250 )); [ "$opt_slots" -lt 1 ] && opt_slots=1
    local ocr=$opt_slots
    [ "$ocr" -gt "$U_OPT" ] && ocr=$U_OPT
    [ "$ocr" -gt 6 ] && ocr=6
    [ -n "${OCR_MAX_WORKERS:-}" ] && ocr="$OCR_MAX_WORKERS"
    export OCR_MAX_WORKERS="$ocr"
    local opt_conc=$(( opt_slots / ocr )); [ "$opt_conc" -lt 1 ] && opt_conc=1
    [ "$opt_conc" -gt "$U_OPT" ] && opt_conc=$U_OPT
    [ -n "${OPTIMIZE_PDF_CONCURRENCY:-}" ] && opt_conc="$OPTIMIZE_PDF_CONCURRENCY"
    export OPTIMIZE_PDF_CONCURRENCY="$opt_conc"

    # convert-from-pdf: ~300 MB per job (pdf2docx/LibreOffice).
    local cf=$(( (MEM_CF - 150) / 300 )); [ "$cf" -lt 1 ] && cf=1
    [ "$cf" -gt "$U_CF" ] && cf=$U_CF
    [ -n "${CONVERT_FROM_PDF_CONCURRENCY:-}" ] && cf="$CONVERT_FROM_PDF_CONCURRENCY"
    export CONVERT_FROM_PDF_CONCURRENCY="$cf"

    # organize-pdf: pdfcpu is pure-Go and cheap, ~96 MB per job; cap 8.
    local org=$(( (MEM_ORG - 64) / 96 )); [ "$org" -lt 1 ] && org=1
    [ "$org" -gt "$U_ORG" ] && org=$U_ORG
    [ "$org" -gt 8 ] && org=8
    [ -n "${ORGANIZE_PDF_CONCURRENCY:-}" ] && org="$ORGANIZE_PDF_CONCURRENCY"
    export ORGANIZE_PDF_CONCURRENCY="$org"

    # redis maxmemory kept under the redis container cap (existing pattern).
    export REDIS_MAXMEMORY="$(( MEM_REDIS * 75 / 100 ))mb"

    echo ""
    printf "  Derived pools: convert-to=%s (unoserver %s)  convert-from=%s  organize=%s  optimize=%s (ocr-workers=%s)\n" \
        "$cto" "$insts" "$cf" "$org" "$opt_conc" "$ocr"
    printf "  redis maxmemory: %s\n" "$REDIS_MAXMEMORY"
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

# Rollback path: retag a previous build and recreate the stack, no rebuild.
# Runs after .env is loaded (compose needs it to interpolate the full file).
if [ -n "$ROLLBACK_SHA" ]; then
    do_rollback "$ROLLBACK_SHA"
    exit 0
fi

# Compute and apply the 70% RAM/CPU budget for THIS host (exports per-service
# *_MEM_LIMIT / *_CPU_LIMIT and derived worker pools; exported env wins over
# the COMPOSE_ENV_FILES values during ${VAR} substitution).
compute_resource_budget
if [ "$DRY_RUN" = "1" ]; then
    echo ""
    print_success "Dry run — budget shown above, no build or deploy performed."
    exit 0
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

# ---------------------------------------------------------------------------
# Single-service mode: rebuild + redeploy only the named service(s) through
# their per-service compose files (extends-based, see
# docs/developer/architecture/compose-files.md). Runs AFTER .env sourcing,
# compute_resource_budget and the JWT secret so the recreated container gets
# the exact same env/limits as a full deploy. The rest of the stack is
# untouched — infra (db/redis/nats/minio) must already be running.
# ---------------------------------------------------------------------------
if [ ${#SERVICES_TO_DEPLOY[@]} -gt 0 ]; then
    export DOCKER_BUILDKIT=1
    export COMPOSE_ENV_FILES="$ROOT_DIR/.env"
    # The per-service file's project sees only one service; the rest of the
    # running stack would be reported as "orphans" — that's expected here.
    export COMPOSE_IGNORE_ORPHANS=1

    for SVC in "${SERVICES_TO_DEPLOY[@]}"; do
        if [ ! -f "$SCRIPT_DIR/docker-compose-$SVC.yml" ]; then
            print_error "Unknown service '$SVC' (no deployment/docker-compose-$SVC.yml)"
            echo "Available services:"
            for f in "$SCRIPT_DIR"/docker-compose-*.yml; do
                b=$(basename "$f" .yml)
                echo "  ${b#docker-compose-}"
            done
            exit 1
        fi
    done

    # Every service builds FROM the shared base images — make sure they exist.
    ensure_base_images

    for SVC in "${SERVICES_TO_DEPLOY[@]}"; do
        SVC_FILE="$SCRIPT_DIR/docker-compose-$SVC.yml"
        print_step "Deploying single service: $SVC"
        STEP_START=$SECONDS
        # `compose up --build` uses the Dockerfile's GO_BUILD_PARALLELISM default (6);
        # to change it, rebuild the base with GO_BUILD_PARALLELISM=N build-base-image.sh.
        # --force-recreate: `up` decides recreation from the service config-hash, not
        # the image content, so a rebuilt-but-same-config service would otherwise be
        # left "up-to-date" (old container, new image unused). Force it so the fresh
        # image always goes live on a single-service redeploy.
        docker compose -f "$SVC_FILE" up -d --build --force-recreate "$SVC"
        print_success "$SVC built + started (took $(( SECONDS - STEP_START ))s)"

        CID=$(docker compose -f "$SVC_FILE" ps -q "$SVC")
        HAS_HC=$(docker inspect --format '{{if .State.Health}}yes{{end}}' "$CID" 2>/dev/null || true)
        if [ "$HAS_HC" != "yes" ]; then
            print_warning "$SVC has no healthcheck — skipping health wait"
            continue
        fi
        echo -n "Waiting for $SVC to become healthy... "
        for i in {1..60}; do
            STATUS=$(docker inspect --format '{{.State.Health.Status}}' "$CID" 2>/dev/null || echo unknown)
            if [ "$STATUS" = "healthy" ]; then
                print_success "$SVC healthy!"
                break
            fi
            if [ $i -eq 60 ]; then
                print_error "$SVC not healthy within 60s (status: $STATUS)"
                docker compose -f "$SVC_FILE" logs "$SVC" | tail -30
                exit 1
            fi
            echo -n "."
            sleep 1
        done
    done

    echo ""
    docker compose -f "$SCRIPT_DIR/docker-compose.yml" ps "${SERVICES_TO_DEPLOY[@]}"
    echo ""
    print_success "Single-service deploy complete: ${SERVICES_TO_DEPLOY[*]}"
    exit 0
fi

# Configuration Notice
print_step "PDF Processing Configuration"
print_success "Using free open-source tools (pdfcpu, LibreOffice, Poppler)"
print_success "Using Go Workspace caching for ultra-fast builds"

# Compose context — MUST be exported before the `down` below, or that command
# runs from the repo root with no compose file discovered and silently no-ops
# (2>/dev/null || true), leaving every old container in place.
export DOCKER_BUILDKIT=1
export COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
export COMPOSE_ENV_FILES="$ROOT_DIR/.env"
# Observability (Grafana/Prometheus/Tempo/otel-collector) ships with every deploy:
# activating its compose profile here means `docker compose down`/`up -d`/`ps`
# below pick it up automatically. Opt out with:  COMPOSE_PROFILES= ./deployment/deploy.sh
export COMPOSE_PROFILES="${COMPOSE_PROFILES:-observability}"

# 1. Stop existing containers — full teardown so every service is recreated from
# its freshly built image on `up` (no stale "up-to-date" containers). Named
# volumes (postgres_data, grafana_data, ...) persist, so data survives.
print_step "Stopping existing containers..."
docker compose down --remove-orphans
print_success "Stopped existing containers"

# 2. Sequential Build Stage (CPU-Safe Mode)
# (GO_SERVICES is defined once near the top so the image-tag/rollback helpers can
# see it too.)
print_step "Building Go Services sequentially..."

# Every service builds FROM fyredocs-go-builder (+ fyredocs-base). Build them once
# up front; the warm Go cache they carry is what makes the per-service builds fast.
ensure_base_images

for SERVICE in "${GO_SERVICES[@]}"; do
    echo -e "${YELLOW}🔨 Starting build for: $SERVICE...${NC}"
    STEP_START=$SECONDS

    docker compose build --build-arg GO_BUILD_PARALLELISM="$GO_BUILD_PARALLELISM" "$SERVICE"

    STEP_DURATION=$(( SECONDS - STEP_START ))
    print_success "$SERVICE build complete (took ${STEP_DURATION}s)"
done

# Tag each freshly built image as fyredocs-<svc>:<git-sha> so this exact build can
# be rolled back to (deploy.sh --rollback=<sha>), then prune old tags to a bound.
tag_built_images
prune_old_image_tags

# 3. Start all services
print_step "Starting all services in detached mode..."
docker compose up -d
print_success "All services are running!"

# 4. Wait for services to be healthy
print_step "Waiting for services to be ready..."

# --- Local PostgreSQL check commented out: using Neon cloud database ---
# echo -n "Waiting for Database... "
# for i in {1..30}; do
#     if docker compose exec -T db pg_isready -U "$POSTGRES_USER" -d "${POSTGRES_DB:-fyredocs}" &> /dev/null; then
#         print_success "Database ready!"
#         break
#     fi
#     if [ $i -eq 30 ]; then
#         print_error "Database failed to start within 30s!"
#         docker compose logs db | tail -20
#         exit 1
#     fi
#     echo -n "."
#     sleep 1
# done
# Provider-agnostic Postgres reachability probe. Works for any DATABASE_URL
# (Neon, RDS, Supabase, a local/in-compose Postgres, ...). Prefers a host psql
# if present; otherwise runs psql inside a throwaway container so the check
# needs no host tooling. The containerized probe joins the compose network, so
# it can reach both external hosts and in-compose db services.
echo -n "Checking database connectivity... "
db_check() {
    [ -n "${DATABASE_URL:-}" ] || return 2
    if command -v psql &> /dev/null; then
        PGCONNECT_TIMEOUT=5 psql "${DATABASE_URL}" -c "SELECT 1" &> /dev/null && return 0
    fi
    if command -v docker &> /dev/null; then
        local net args
        net=$(docker network ls --format '{{.Name}}' | grep -E '(^|_)fyredocs_net$' | head -1)
        args=(run --rm -e PGCONNECT_TIMEOUT=5)
        [ -n "$net" ] && args+=(--network "$net")
        args+=(postgres:16-alpine psql "${DATABASE_URL}" -c "SELECT 1")
        docker "${args[@]}" &> /dev/null && return 0
    fi
    return 1
}
if db_check; then
    print_success "Database reachable!"
elif [ -z "${DATABASE_URL:-}" ]; then
    print_warning "DATABASE_URL not set — skipping connectivity check"
else
    print_warning "Could not verify database connectivity — continuing anyway (services run their own readiness checks)"
fi

echo -n "Waiting for edge (Caddy → API Gateway)... "
for i in {1..30}; do
    if curl -s http://localhost/healthz &> /dev/null; then
        print_success "Edge + API Gateway ready!"
        break
    fi
    if [ $i -eq 30 ]; then
        print_error "Edge failed to start within 30s!"
        docker compose logs caddy api-gateway | tail -30
        exit 1
    fi
    echo -n "."
    sleep 1
done

# 5. Post-Deployment Cleanup
# NOTE: keep this as `docker image prune -f` (dangling images only). Do NOT change it
# to `docker builder prune` or `docker system prune -a` — those wipe the BuildKit build
# cache and the fyredocs-go-builder image, reintroducing the ~20-min cold Go compile.
# Previous builds are NOT lost to this prune: each deploy SHA-tags its images
# (fyredocs-<svc>:<sha>), so they keep a tag and survive; rollback targets remain
# available (bounded by IMAGE_TAG_RETAIN, pruned above).
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
# Edge URL: PUBLIC_DOMAIN set → Caddy terminates TLS on that domain;
# unset (or a bare :port from the compose default) → plain HTTP locally.
case "${PUBLIC_DOMAIN:-}" in
    ""|:*) EDGE_URL="http://localhost" ;;
    *)     EDGE_URL="https://$PUBLIC_DOMAIN" ;;
esac

# DB label from DATABASE_URL: services talk to whatever it points at
# (in-compose db, Neon, RDS, ...) — don't hardcode a provider here.
if [ -n "${DATABASE_URL:-}" ]; then
    DB_HOST=$(echo "$DATABASE_URL" | sed -E 's|.*@([^/:?]+).*|\1|')
    if [ "$DB_HOST" = "db" ]; then
        DB_LABEL="in-compose Postgres (db:5432)"
    else
        DB_LABEL="external ($DB_HOST)"
    fi
else
    DB_LABEL="not configured (DATABASE_URL unset)"
fi

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "📋 Service Endpoints:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  🌐 App (Caddy edge):      $EDGE_URL  (SPA + APIs under /api, /auth, /admin)"
echo "  🌐 API Gateway:           internal only (caddy → api-gateway:${API_GATEWAY_PORT:-8080})"
echo "  🔑 Auth Service:          internal only (auth-service:8086)"
echo "  📤 Job Service:           internal only (job-service:8081, + in-process cleanup sweeps)"
echo "  📄 Convert-From-PDF:      internal only (convert-from-pdf:8082)"
echo "  📑 Convert-To-PDF:        internal only (convert-to-pdf:8083)"
echo "  📋 Organize-PDF:          internal only (organize-pdf:8084)"
echo "  🔧 Optimize-PDF:          internal only (optimize-pdf:8085)"
echo "  📊 Analytics:             internal only (analytics-service:8087)"
echo "  🗂️  Document Service:      internal only (document-service:8089)"
echo "  👤 User Service:          internal only (user-service:8090)"
echo "  🔔 Notification Service:  internal only (notification-service:8091)"
echo "  📦 MinIO (S3):            internal (minio:9000) — console http://127.0.0.1:9001, objects via edge /uploads /outputs"
echo "  📨 NATS:                  internal only (nats:4222)"
echo "  🔴 Redis:                 internal only (redis:6379)"
echo "  🗄️  PostgreSQL:            $DB_LABEL"
echo "  📈 Observability:         started with this deploy (super-admin: $EDGE_URL/grafana)"
echo "                            → Grafana http://127.0.0.1:3000, Prometheus http://127.0.0.1:9090"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔧 Useful Commands:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Deploy one service:    ./deployment/deploy.sh <service>   (e.g. auth-service)"
echo "  Disable observability: COMPOSE_PROFILES= ./deployment/deploy.sh"
echo "  Compose files layout:  docs/developer/architecture/compose-files.md"
echo "  View logs:             docker compose -f deployment/docker-compose.yml --env-file .env logs -f"
echo "  View specific service: docker compose -f deployment/docker-compose.yml --env-file .env logs -f api-gateway"
echo "  Restart services:      docker compose -f deployment/docker-compose.yml --env-file .env restart"
echo "  Stop all:              docker compose -f deployment/docker-compose.yml --env-file .env --profile observability down"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔐 Security Info:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  JWT Secret stored in:  $JWT_SECRET_FILE"
echo "  ⏱️  Total Deploy Time:  ${MINUTES}m ${SECONDS_REM}s"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""