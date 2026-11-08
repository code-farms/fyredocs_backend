#!/bin/bash
# measure-images.sh — reproducible baseline measurements for the document workers.
#
# Measures, against the images tagged $TAG (default baseline-2026-11):
#   1. image sizes + top layers            → $RAW_DIR/image-sizes.tsv, layers-<img>.txt
#   2. stripped binary sizes (linux/arm64 + linux/amd64 cross-builds) → raw/binary-sizes.tsv
#   3. tool versions inside each worker image → raw/tool-versions.txt
#   4. idle RSS of each worker (one at a time, against the essentials stack) → raw/idle-rss.tsv
#   5. unoserver cold start (container start → first successful unoconvert) → raw/cold-start.tsv
#   6. single-conversion timings + peak RSS: LibreOffice warm (unoconvert) vs cold
#      (libreoffice --headless) on docx/xlsx/pptx small|medium|large; Ghostscript compress and
#      Tesseract OCR on pdf / scanned-pdf fixtures → raw/conversions.tsv
#
# Every timing is repeated $RUNS times (default 3); the TSV keeps all runs — the report takes the median.
# Nothing here changes the repository. Requires: docker, go, the k6 fixtures
# (bash scripts/k6/fixtures/generate.sh). Run from anywhere:
#   bash docs/developer/architecture/next-generation-document-engine/benchmarks/measure-images.sh
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../../../../.." && pwd)"          # fyredocs_backend
# RAW_DIR keeps each measurement run in its own folder (default raw/, the
# 2026-11-07 baseline); pass e.g. RAW_DIR=$HERE/raw-2026-11-08 for a re-measure.
RAW="${RAW_DIR:-$HERE/raw}"; mkdir -p "$RAW"
TAG="${TAG:-baseline-2026-11}"
RUNS="${RUNS:-3}"
FIX="$ROOT/scripts/k6/fixtures/out"
OVERRIDE="${COMPOSE_OVERRIDE:-}"
COMPOSE="docker compose -f $ROOT/deployment/docker-compose.yml ${OVERRIDE:+-f $OVERRIDE} --env-file $ROOT/.env"
STEPS="${STEPS:-1,2,3,4,5,6}"
step() { case ",$STEPS," in *,$1,*) return 0;; *) return 1;; esac; }
WORKERS="convert-to-pdf convert-from-pdf organize-pdf optimize-pdf"
ALL="api-gateway auth-service job-service convert-from-pdf convert-to-pdf organize-pdf optimize-pdf analytics-service document-service user-service notification-service ai-service"
log() { echo "[$(date '+%H:%M:%S')] $*" >&2; }
now_ms() { python3 -c 'import time; print(int(time.time()*1000))'; }

if step 1; then
# ── 1. image sizes ────────────────────────────────────────────────────────────
log "1. image sizes"
{
  printf 'image\ttag\tsize_bytes\tcreated\n'
  for i in fyredocs-base fyredocs-go-builder fyredocs-test-binaries $(for s in $ALL; do echo fyredocs-$s; done); do
    docker image inspect "$i:$TAG" --format "$i	$TAG	{{.Size}}	{{.Created}}" 2>/dev/null || printf '%s\t%s\tMISSING\t-\n' "$i" "$TAG"
  done
} > "$RAW/image-sizes.tsv"
for s in $WORKERS; do docker history --no-trunc --format '{{.Size}}\t{{.CreatedBy}}' "fyredocs-$s:$TAG" 2>/dev/null | head -40 > "$RAW/layers-$s.txt"; done
docker history --no-trunc --format '{{.Size}}\t{{.CreatedBy}}' "fyredocs-base:$TAG" 2>/dev/null | head -40 > "$RAW/layers-fyredocs-base.txt"

fi
if step 2; then
# ── 2. binary sizes ───────────────────────────────────────────────────────────
log "2. binary sizes (cross-compiled, CGO_ENABLED=0 -trimpath -ldflags='-s -w')"
BIN=$(mktemp -d)
{
  printf 'service\tgoos_goarch\tsize_bytes\n'
  for s in $ALL; do
    for arch in arm64 amd64; do
      if (cd "$ROOT/$s" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags='-s -w' -o "$BIN/$s-$arch" . 2>>"$RAW/binary-build.log"); then
        printf '%s\tlinux_%s\t%s\n' "$s" "$arch" "$(stat -f %z "$BIN/$s-$arch")"
      else
        printf '%s\tlinux_%s\tBUILD_FAILED\n' "$s" "$arch"
      fi
    done
  done
} > "$RAW/binary-sizes.tsv"
rm -rf "$BIN"

fi
if step 3; then
# ── 3. tool versions inside images ────────────────────────────────────────────
log "3. tool versions"
{
  echo "== convert-to-pdf (fyredocs-base) =="
  docker run --rm --entrypoint sh "fyredocs-convert-to-pdf:$TAG" -c 'cat /etc/alpine-release; libreoffice --version 2>/dev/null | head -1; pip3 show unoserver 2>/dev/null | grep -i ^version; pdftoppm -v 2>&1 | head -1; python3 --version; fc-list 2>/dev/null | wc -l; ls /usr/share/fonts 2>/dev/null' 2>&1
  echo "== convert-from-pdf =="
  docker run --rm --entrypoint sh "fyredocs-convert-from-pdf:$TAG" -c 'cat /etc/debian_version; libreoffice --version 2>/dev/null | head -1; gs --version; pdftoppm -v 2>&1 | head -1; python3 --version; pip3 show pdf2docx PyMuPDF pdfplumber openpyxl 2>/dev/null | grep -iE "^(name|version)"; fc-list | wc -l' 2>&1
  echo "== optimize-pdf =="
  docker run --rm --entrypoint sh "fyredocs-optimize-pdf:$TAG" -c 'cat /etc/alpine-release; gs --version; tesseract --version 2>&1 | head -1; tesseract --list-langs 2>&1 | tr "\n" " "; echo; pdftoppm -v 2>&1 | head -1' 2>&1
  echo "== organize-pdf =="
  docker run --rm --entrypoint sh "fyredocs-organize-pdf:$TAG" -c 'cat /etc/alpine-release; tesseract --version 2>&1 | head -1; tesseract --list-langs 2>&1 | tr "\n" " "; echo' 2>&1
} > "$RAW/tool-versions.txt"

fi
if step 4; then
# ── 4+5. idle RSS and cold start, one worker at a time ─────────────────────────
log "4/5. idle RSS + cold start (essentials stack: db redis nats minio)"
$COMPOSE up -d db pgbouncer redis nats minio minio-init >> "$RAW/compose.log" 2>&1
for i in $(seq 1 60); do $COMPOSE ps --format "{{.Name}} {{.Health}}" 2>/dev/null | grep -q "db-1 healthy" && break; sleep 2; done
$COMPOSE ps --format "{{.Name}}\t{{.Status}}" >> "$RAW/compose.log"
{
  printf 'worker\tidle_rss_bytes\tidle_cpu_pct\tsettle_seconds\n'
} > "$RAW/idle-rss.tsv"
{ printf 'worker\trun\tcold_start_ms\tnotes\n'; } > "$RAW/cold-start.tsv"
for s in $WORKERS; do
  for r in $(seq 1 "$RUNS"); do
    $COMPOSE rm -sf "$s" >> "$RAW/compose.log" 2>&1
    t0=$(now_ms)
    $COMPOSE up -d --no-deps "$s" >> "$RAW/compose.log" 2>&1
    cid=$($COMPOSE ps -q "$s" | head -1)
    # readiness: /readyz (all workers) — for convert-to-pdf also wait for the unoserver "ready" log line
    ready=""; for i in $(seq 1 120); do
      if docker exec "$cid" sh -c 'wget -qO- http://127.0.0.1:${PORT:-8080}/readyz >/dev/null 2>&1 || python3 -c "import urllib.request,os;urllib.request.urlopen(\"http://127.0.0.1:%s/readyz\"%os.environ.get(\"PORT\",\"8080\"))" >/dev/null 2>&1' 2>/dev/null; then ready=$(now_ms); break; fi
      sleep 0.5
    done
    note="readyz"
    if [ "$s" = convert-to-pdf ]; then
      # as shipped: did the unoserver pool come up? (entrypoint logs a WARNING when it did not)
      if docker logs "$cid" 2>&1 | grep -q "unoserver did not become ready"; then note="readyz;unoserver-pool-DEAD-as-shipped"; else note="readyz;unoserver-pool-up"; fi
    fi
    if [ -n "$ready" ]; then printf '%s\t%s\t%s\t%s\n' "$s" "$r" "$((ready - t0))" "$note" >> "$RAW/cold-start.tsv"; else printf '%s\t%s\tTIMEOUT\t-\n' "$s" "$r" >> "$RAW/cold-start.tsv"; fi
    if [ "$r" = 1 ]; then
      sleep 20
      docker stats --no-stream --format '{{.MemUsage}}\t{{.CPUPerc}}' "$cid" | awk -v w="$s" -F'\t' '{print w"\t"$1"\t"$2"\t20"}' >> "$RAW/idle-rss.tsv"
      docker logs "$cid" > "$RAW/startlog-$s.txt" 2>&1
    fi
  done
done

fi
if step 6; then
# ── 6. conversions: warm/cold LibreOffice, gs, tesseract ──────────────────────
# Runs the worker IMAGES directly (no compose, no DB/NATS) with the k6 fixtures bind-mounted read-only
# and the same 1 GiB memory limit + tmpfs /tmp as production compose. NOTE: `docker cp` cannot write into
# a tmpfs mount, which is why an earlier compose-exec variant of this step failed with "No such file".
log "6. single-conversion timings"
{ printf 'engine\tfixture\tsize_bytes\trun\twall_ms\tpeak_rss_bytes\tstatus\n'; } > "$RAW/conversions.tsv"
peak_rss() { # cid cmd... ; samples docker stats while the command runs; prints wall_ms<TAB>peak_bytes<TAB>status
  local cid="$1"; shift
  local peak=0 t0 t1 st
  ( while :; do docker stats --no-stream --format '{{.MemUsage}}' "$cid" 2>/dev/null | awk '{print $1}'; sleep 0.25; done ) > "$RAW/.samples" &
  local sp=$!
  t0=$(now_ms); docker exec "$cid" sh -c "$*" > /dev/null 2>>"$RAW/conversions.log"; st=$?; t1=$(now_ms)
  kill $sp 2>/dev/null; wait $sp 2>/dev/null
  peak=$(python3 - "$RAW/.samples" <<'PY2'
import sys,re
m=0
for l in open(sys.argv[1]):
    l=l.strip(); r=re.match(r'([\d.]+)([KMG]i?B)',l)
    if not r: continue
    v=float(r.group(1)); u=r.group(2)[0]
    v*= {'K':1024,'M':1024**2,'G':1024**3}[u]
    m=max(m,v)
print(int(m))
PY2
)
  printf '%s\t%s\t%s' "$((t1-t0))" "$peak" "$([ $st = 0 ] && echo ok || echo FAIL:$st)"
}
# LibreOffice --convert-to exits 0 even when it fails ("source file could not be loaded"), so success is
# defined as "output PDF exists and is non-empty"; the helper below wraps a command with that check.
lo_ok() { echo "$1 && test -s $2"; }
docker rm -f bench-ctp >/dev/null 2>&1
docker run -d --name bench-ctp --entrypoint sh -m 1g --tmpfs /tmp:size=1g -v "$FIX:/fx:ro" "fyredocs-convert-to-pdf:$TAG" -c 'mkdir -p /tmp/out; sleep 36000' >/dev/null
cid=bench-ctp; sleep 2
# daemon start with the CORRECTED arguments (path, distinct --uno-port); time until first unoconvert succeeds
for r in $(seq 1 "$RUNS"); do
  docker exec "$cid" sh -c 'pkill -f unoserver; pkill -f soffice; sleep 2; rm -rf /tmp/uno-fixed-* /tmp/out/probe.pdf' >/dev/null 2>&1
  t0=$(now_ms)
  docker exec -d "$cid" sh -c 'unoserver --interface 127.0.0.1 --port 2002 --uno-port 2102 --user-installation /tmp/uno-fixed-2002 >/tmp/uno-2002.log 2>&1'
  docker exec -d "$cid" sh -c 'unoserver --interface 127.0.0.1 --port 2003 --uno-port 2103 --user-installation /tmp/uno-fixed-2003 >/tmp/uno-2003.log 2>&1'
  st=TIMEOUT; for i in $(seq 1 120); do if docker exec "$cid" sh -c 'timeout 20 unoconvert --host 127.0.0.1 --port 2002 --convert-to pdf /fx/odt/small.odt /tmp/out/probe.pdf >/dev/null 2>&1 && test -s /tmp/out/probe.pdf'; then st=ok; break; fi; sleep 1; done
  t1=$(now_ms)
  rss=$(docker stats --no-stream --format '{{.MemUsage}}' "$cid" | awk '{print $1}')
  printf 'unoserver-daemon-start-corrected-arg(2 daemons)\t-\t0\t%s\t%s\t%s\t%s\n' "$r" "$((t1-t0))" "$rss" "$st" >> "$RAW/conversions.tsv"
done
docker exec "$cid" sh -c 'cat /tmp/uno-2002.log' > "$RAW/unoserver-corrected-2002.log" 2>&1
for f in docx/small.docx docx/medium.docx docx/large.docx xlsx/small.xlsx xlsx/medium.xlsx pptx/small.pptx pptx/medium.pptx html/small.html odt/small.odt; do
  [ -f "$FIX/$f" ] || continue
  sz=$(stat -f %z "$FIX/$f"); b=$(basename "$f")
  for r in $(seq 1 "$RUNS"); do
    docker exec "$cid" sh -c "rm -f /tmp/out/$b.warm.pdf /tmp/out/${b%.*}.pdf" >/dev/null 2>&1
    res=$(peak_rss "$cid" "$(lo_ok "timeout 600 unoconvert --host 127.0.0.1 --port 2002 --convert-to pdf /fx/$f /tmp/out/$b.warm.pdf" "/tmp/out/$b.warm.pdf")")
    printf 'libreoffice-warm-unoconvert\t%s\t%s\t%s\t%s\n' "$f" "$sz" "$r" "$res" >> "$RAW/conversions.tsv"
    res=$(peak_rss "$cid" "$(lo_ok "timeout 600 libreoffice --headless -env:UserInstallation=file:///tmp/lo-cold-$r --convert-to pdf --outdir /tmp/out /fx/$f" "/tmp/out/${b%.*}.pdf")")
    printf 'libreoffice-cold-headless\t%s\t%s\t%s\t%s\n' "$f" "$sz" "$r" "$res" >> "$RAW/conversions.tsv"
  done
done
docker rm -f bench-ctp >/dev/null 2>&1
docker rm -f bench-opt >/dev/null 2>&1
docker run -d --name bench-opt --entrypoint sh -m 1g --tmpfs /tmp:size=1g -v "$FIX:/fx:ro" "fyredocs-optimize-pdf:$TAG" -c 'mkdir -p /tmp/out; sleep 36000' >/dev/null
cid=bench-opt; sleep 2
for f in pdf/small.pdf pdf/medium.pdf pdf/large.pdf; do
  [ -f "$FIX/$f" ] || continue; sz=$(stat -f %z "$FIX/$f"); b=$(basename "$f")
  for r in $(seq 1 "$RUNS"); do
    res=$(peak_rss "$cid" "gs -dNOPAUSE -dBATCH -dSAFER -sDEVICE=pdfwrite -dCompatibilityLevel=1.4 -dPDFSETTINGS=/ebook -dDetectDuplicateImages=true -dCompressFonts=true -dDownsampleColorImages=true -dColorImageResolution=150 -sOutputFile=/tmp/out/$b.gs.pdf /fx/$f && test -s /tmp/out/$b.gs.pdf")
    printf 'ghostscript-compress-ebook\t%s\t%s\t%s\t%s\n' "$f" "$sz" "$r" "$res" >> "$RAW/conversions.tsv"
  done
done
for f in scanned-pdf/small.pdf scanned-pdf/medium.pdf scanned-pdf/large.pdf; do
  [ -f "$FIX/$f" ] || continue; sz=$(stat -f %z "$FIX/$f")
  pages=$(docker exec "$cid" sh -c "pdfinfo /fx/$f 2>/dev/null | awk '/^Pages:/{print \$2}'")
  for r in $(seq 1 "$RUNS"); do
    res=$(peak_rss "$cid" "rm -rf /tmp/ocr && mkdir -p /tmp/ocr && pdftoppm -png -r 150 /fx/$f /tmp/ocr/page && for p in /tmp/ocr/page*.png; do tesseract \$p \${p%.png} -l eng pdf txt >/dev/null 2>&1 || exit 1; done")
    printf 'pdftoppm150+tesseract-eng-sequential(%s pages)\t%s\t%s\t%s\t%s\n' "${pages:-?}" "$f" "$sz" "$r" "$res" >> "$RAW/conversions.tsv"
  done
done
docker rm -f bench-opt >/dev/null 2>&1
fi
rm -f "$RAW/.samples"
log "done — raw results in $RAW"
