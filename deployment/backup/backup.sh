#!/bin/sh
# Periodic logical backup of the co-located Postgres to an external S3-compatible
# bucket (Cloudflare R2 / Backblaze B2 / S3 / MinIO), via rclone. Runs as a
# long-lived sidecar: dump -> gzip -> upload -> prune old, then sleep. Failures
# are logged and retried next cycle — the loop never exits on a backup error.
#
# The rclone remote is named "dest" and configured entirely through
# RCLONE_CONFIG_DEST_* env vars (see docker-compose). No rclone.conf file needed.
set -u

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${BACKUP_S3_BUCKET:?BACKUP_S3_BUCKET is required}"
BACKUP_PREFIX="${BACKUP_PREFIX:-postgres/}"
BACKUP_INTERVAL="${BACKUP_INTERVAL:-3600}"
BACKUP_RETAIN="${BACKUP_RETAIN:-48}"

log() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) [db-backup] $*"; }

log "started: bucket=$BACKUP_S3_BUCKET prefix=$BACKUP_PREFIX interval=${BACKUP_INTERVAL}s retain=$BACKUP_RETAIN"

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

	sleep "$BACKUP_INTERVAL"
done
