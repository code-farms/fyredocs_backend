#!/usr/bin/env bash
#
# migrate-files-to-minio.sh — one-off migration of legacy on-disk files into
# MinIO object storage.
#
# BACKGROUND
#   Before the object-storage migration, services wrote files to a shared bind
#   mount and stored ABSOLUTE container paths in file_metadata.path, e.g.
#       /app/uploads/<jobId>/<fileName>          (kind = "input")
#       /app/outputs/<prefix>_<jobId>_<ts>.<ext> (kind = "output")
#   On the host those map to ../files/uploads and ../files/outputs.
#
#   After the migration, file_metadata.path holds OBJECT KEYS:
#       uploads/...   keys live in the uploads bucket
#       jobs/<jobId>/<baseName> keys live in the outputs bucket
#   job-service's cleanup loop skips rows whose path still starts with "/" (legacy)
#   and logs a pointer to this script.
#
# WHAT THIS SCRIPT DOES (per legacy row, i.e. path LIKE '/%'):
#   1. Maps the container path to the host file
#        /app/uploads/X  -> $FILES_ROOT/uploads/X
#        /app/outputs/X  -> $FILES_ROOT/outputs/X
#   2. Derives the target bucket + object key
#        /app/uploads/<jobId>/<file> -> uploads : uploads/<jobId>/<file>
#        /app/outputs/<base>         -> outputs : jobs/<jobId>/<base>
#      (<jobId> is the job_id column of the row — outputs are re-keyed under
#       jobs/<jobId>/ to match the new layout)
#   3. Copies the file into MinIO with `mc cp`
#   4. Updates file_metadata.path to the new object key with psql
#
# SAFETY
#   - DRY-RUN BY DEFAULT: prints every action without copying or updating.
#     Pass --execute to apply.
#   - Idempotent: rows already migrated (path not starting with "/") are
#     never selected; re-running after a partial failure is safe.
#   - Missing host files are reported and SKIPPED (the DB row is left
#     untouched so nothing is lost; investigate manually).
#
# PREREQUISITES
#   - mc (MinIO client) installed and an alias configured for the target
#     MinIO, e.g.:  mc alias set fyredocs http://localhost:9001 <root-user> <root-pass>
#     (or run from a host that can reach minio:9000 inside the compose network)
#   - psql with access to the application database
#
# USAGE
#   DATABASE_URL='postgres://user:pass@host/db' \
#   MC_ALIAS=fyredocs \
#   FILES_ROOT=../files \
#   ./scripts/migrate-files-to-minio.sh [--execute]
#
set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration (override via environment)
# ---------------------------------------------------------------------------
DATABASE_URL="${DATABASE_URL:?DATABASE_URL is required (postgres connection string)}"
MC_ALIAS="${MC_ALIAS:-fyredocs}"                 # mc alias pointing at MinIO
FILES_ROOT="${FILES_ROOT:-../files}"             # host dir that was bind-mounted
BUCKET_UPLOADS="${S3_BUCKET_UPLOADS:-uploads}"
BUCKET_OUTPUTS="${S3_BUCKET_OUTPUTS:-outputs}"

DRY_RUN=1
if [[ "${1:-}" == "--execute" ]]; then
  DRY_RUN=0
elif [[ -n "${1:-}" ]]; then
  echo "Unknown argument: $1 (only --execute is supported)" >&2
  exit 2
fi

run() {
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "[dry-run] $*"
  else
    "$@"
  fi
}

echo "== migrate-files-to-minio =="
echo "   mode:        $([[ $DRY_RUN -eq 1 ]] && echo DRY-RUN || echo EXECUTE)"
echo "   mc alias:    $MC_ALIAS"
echo "   files root:  $FILES_ROOT"
echo "   buckets:     $BUCKET_UPLOADS / $BUCKET_OUTPUTS"
echo

migrated=0
skipped=0
failed=0

# Select legacy rows: id|job_id|kind|path  (ASCII unit separator-safe enough:
# paths never contain '|' in this system; -A -t gives bare pipe-separated rows)
while IFS='|' read -r id job_id kind path; do
  [[ -z "$id" ]] && continue

  # ----- 1. container path -> host path ------------------------------------
  case "$path" in
    /app/uploads/*) host_file="$FILES_ROOT/uploads/${path#/app/uploads/}" ;;
    /app/outputs/*) host_file="$FILES_ROOT/outputs/${path#/app/outputs/}" ;;
    *)
      echo "SKIP  $id: unrecognized legacy path prefix: $path"
      skipped=$((skipped + 1))
      continue
      ;;
  esac

  if [[ ! -f "$host_file" ]]; then
    echo "SKIP  $id: host file missing: $host_file (row left untouched)"
    skipped=$((skipped + 1))
    continue
  fi

  # ----- 2. derive bucket + object key -------------------------------------
  if [[ "$path" == /app/uploads/* ]]; then
    bucket="$BUCKET_UPLOADS"
    key="uploads/${path#/app/uploads/}"
  else
    bucket="$BUCKET_OUTPUTS"
    base="$(basename "$path")"
    key="jobs/${job_id}/${base}"
  fi

  echo "MOVE  $id ($kind): $host_file -> $bucket/$key"

  # ----- 3. copy into MinIO -------------------------------------------------
  if ! run mc cp "$host_file" "$MC_ALIAS/$bucket/$key"; then
    echo "FAIL  $id: mc cp failed; DB row left untouched" >&2
    failed=$((failed + 1))
    continue
  fi

  # ----- 4. point the DB row at the object key ------------------------------
  # Path update only happens after a successful copy, so an interrupted run
  # leaves every row either fully migrated or fully legacy.
  if ! run psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q \
      -c "UPDATE file_metadata SET path = '$key' WHERE id = '$id';"; then
    echo "FAIL  $id: psql update failed (object copied; re-run is safe — copy is idempotent)" >&2
    failed=$((failed + 1))
    continue
  fi

  migrated=$((migrated + 1))
done < <(psql "$DATABASE_URL" -A -t -F '|' \
  -c "SELECT id, job_id, kind, path FROM file_metadata WHERE path LIKE '/%' ORDER BY created_at;")

echo
echo "== done: migrated=$migrated skipped=$skipped failed=$failed =="
if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "(dry-run — re-run with --execute to apply)"
fi
[[ "$failed" -eq 0 ]]
