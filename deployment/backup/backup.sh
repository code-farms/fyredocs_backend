#!/bin/sh
# Periodic logical backup of the co-located Postgres to an external S3-compatible
# bucket (Cloudflare R2 / Backblaze B2 / S3 / MinIO), via rclone. Runs as a
# long-lived sidecar: dump -> gzip -> upload -> prune old, then sleep. Failures
# are logged and retried next cycle — the loop never exits on a backup error.
#
# Two rclone remotes, both configured entirely via env vars (no rclone.conf):
#   dest = the offsite backup bucket (RCLONE_CONFIG_DEST_*)
#   src  = the local MinIO, for mirroring file buckets (RCLONE_CONFIG_SRC_*)
set -u

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${BACKUP_S3_BUCKET:?BACKUP_S3_BUCKET is required}"
BACKUP_PREFIX="${BACKUP_PREFIX:-postgres/}"
BACKUP_INTERVAL="${BACKUP_INTERVAL:-3600}"
BACKUP_RETAIN="${BACKUP_RETAIN:-48}"
# Space-separated MinIO buckets to mirror offsite (empty = DB-only, no file backup).
BACKUP_FILES_BUCKETS="${BACKUP_FILES_BUCKETS:-}"
BACKUP_FILES_PREFIX="${BACKUP_FILES_PREFIX:-minio/}"
# When true, only registered (signed-in) users' outputs are backed up; guest
# jobs' objects (jobs/<id>/**, where processing_jobs.user_id IS NULL) are excluded.
BACKUP_FILES_REGISTERED_ONLY="${BACKUP_FILES_REGISTERED_ONLY:-false}"

log() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) [db-backup] $*"; }

log "started: bucket=$BACKUP_S3_BUCKET prefix=$BACKUP_PREFIX interval=${BACKUP_INTERVAL}s retain=$BACKUP_RETAIN files=[${BACKUP_FILES_BUCKETS}]"

while true; do
	TS="$(date -u +%Y%m%dT%H%M%SZ)"
	FILE="db-${TS}.sql.gz"
	TMP="/tmp/${FILE}"

	if pg_dump "$DATABASE_URL" --no-owner --no-privileges | gzip >"$TMP"; then
		if rclone copyto "$TMP" "dest:${BACKUP_S3_BUCKET}/${BACKUP_PREFIX}${FILE}"; then
			SIZE="$(wc -c <"$TMP" 2>/dev/null || echo '?')"
			log "backup ok: ${FILE} (${SIZE} bytes)"
			touch /tmp/last_success

			# Retention: keep the newest $BACKUP_RETAIN objects (timestamped names
			# sort chronologically), delete anything older. BusyBox head has no
			# negative-count support, so compute how many to drop explicitly.
			ALL="$(rclone lsf "dest:${BACKUP_S3_BUCKET}/${BACKUP_PREFIX}" 2>/dev/null | grep '\.sql\.gz$' | sort)"
			TOTAL="$(printf '%s\n' "$ALL" | grep -c .)"
			DEL=$(( TOTAL - BACKUP_RETAIN ))
			if [ "$DEL" -gt 0 ]; then
				printf '%s\n' "$ALL" | head -n "$DEL" | while read -r old; do
					[ -n "$old" ] || continue
					rclone deletefile "dest:${BACKUP_S3_BUCKET}/${BACKUP_PREFIX}${old}" 2>/dev/null \
						&& log "pruned old backup: ${old}"
				done
			fi
		else
			log "UPLOAD FAILED: ${FILE}"
		fi
		rm -f "$TMP"
	else
		log "pg_dump FAILED"
		rm -f "$TMP"
	fi

	# Mirror MinIO file buckets offsite (outputs by default). rclone sync makes
	# the backup match the live bucket: new/changed objects are copied, objects
	# removed from MinIO (expired/cleaned) are removed from the backup too, so
	# storage stays ~= current bucket size. File-sync failures are logged but do
	# not affect the DB-backup healthcheck marker above.
	#
	# Registered-only mode: exclude guest jobs' objects (jobs/<id>/**, where
	# processing_jobs.user_id IS NULL). The whole bucket stays in sync scope minus
	# those prefixes, so registered files are still mirrored + pruned. On a DB
	# query error, fall back to an unfiltered sync (a complete registered backup
	# beats none; a transient guest object self-prunes at its 30-min expiry).
	EXCLUDE_ARGS=""
	if [ "$BACKUP_FILES_REGISTERED_ONLY" = "true" ]; then
		if psql "$DATABASE_URL" -tAc "SELECT id FROM processing_jobs WHERE user_id IS NULL" 2>/tmp/guest_q.err \
			| sed '/^$/d; s#^#/jobs/#; s#$#/**#' > /tmp/guest_excludes.txt; then
			EXCLUDE_ARGS="--exclude-from /tmp/guest_excludes.txt"
			log "registered-only: excluding $(grep -c . /tmp/guest_excludes.txt) guest job prefix(es)"
		else
			log "registered-only: guest-list query FAILED, falling back to full sync ($(cat /tmp/guest_q.err))"
		fi
	fi

	for b in $BACKUP_FILES_BUCKETS; do
		[ -n "$b" ] || continue
		if rclone sync "src:${b}" "dest:${BACKUP_S3_BUCKET}/${BACKUP_FILES_PREFIX}${b}/" $EXCLUDE_ARGS; then
			log "files synced: ${b}"
		else
			log "FILE SYNC FAILED: ${b}"
		fi
	done

	sleep "$BACKUP_INTERVAL"
done
