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
# Empty-source protection: never overwrite/prune a good backup with empty data.
# Skip the DB backup if the users table has fewer than BACKUP_MIN_USERS rows
# (server came back with a wiped/fresh DB). Optional BACKUP_MAX_DELETE bounds how
# many objects a single file sync may delete. BACKUP_ALERT_WEBHOOK_URL (if set)
# receives a POST when a guard trips.
BACKUP_MIN_USERS="${BACKUP_MIN_USERS:-1}"
BACKUP_MAX_DELETE="${BACKUP_MAX_DELETE:-}"
BACKUP_ALERT_WEBHOOK_URL="${BACKUP_ALERT_WEBHOOK_URL:-}"
# Brand the Discord backup embed to match the monitoring alerts (shared/discord).
ENVIRONMENT="${ENVIRONMENT:-}"
DISCORD_ALERT_ICON_URL="${DISCORD_ALERT_ICON_URL:-}"

log() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) [db-backup] $*"; }

# alert logs an ALERT line and, when a webhook is configured, POSTs to it. For a
# native Discord webhook it sends the same executive-style embed the monitoring
# alerts use (orange card, brand author/footer, timestamp); for a Slack-compat
# endpoint (URL ends in /slack) it falls back to {"text":...}. Never fails the cycle.
alert() {
	log "ALERT: $1"
	[ -n "$BACKUP_ALERT_WEBHOOK_URL" ] || return 0
	# JSON-escape the message (backslash, quote; collapse newlines to spaces).
	esc=$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | tr '\n\r' '  ')
	case "$BACKUP_ALERT_WEBHOOK_URL" in
		*/slack)
			body="{\"text\":\"[fyredocs db-backup] $esc\"}"
			;;
		*)
			ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
			footer="Fyredocs"
			[ -n "$ENVIRONMENT" ] && footer="Fyredocs • $ENVIRONMENT"
			author="{\"name\":\"Fyredocs Backups\""
			foot="{\"text\":\"$footer\""
			if [ -n "$DISCORD_ALERT_ICON_URL" ]; then
				author="$author,\"icon_url\":\"$DISCORD_ALERT_ICON_URL\""
				foot="$foot,\"icon_url\":\"$DISCORD_ALERT_ICON_URL\""
			fi
			author="$author}"
			foot="$foot}"
			# color 15241517 = 0xE8912D (orange) — backup alerts are warnings.
			body="{\"embeds\":[{\"title\":\"💾  Backup Alert\",\"description\":\"$esc\",\"color\":15241517,\"author\":$author,\"footer\":$foot,\"timestamp\":\"$ts\"}]}"
			;;
	esac
	curl -m 10 -sS -X POST -H 'Content-Type: application/json' -d "$body" \
		"$BACKUP_ALERT_WEBHOOK_URL" >/dev/null 2>&1 || log "webhook POST failed"
}

log "started: bucket=$BACKUP_S3_BUCKET prefix=$BACKUP_PREFIX interval=${BACKUP_INTERVAL}s retain=$BACKUP_RETAIN files=[${BACKUP_FILES_BUCKETS}]"

while true; do
	TS="$(date -u +%Y%m%dT%H%M%SZ)"
	FILE="db-${TS}.sql.gz"
	TMP="/tmp/${FILE}"

	# Empty-source guard: only back up the DB when it actually has data. A wiped/
	# fresh DB (e.g. server came back with an empty volume) must NOT overwrite or
	# prune the good snapshots — skip + alert instead, and auto-resume when data
	# returns. `users` is the signal (AutoMigrate/seedPlans re-create the schema
	# and subscription_plans on a fresh DB, but never users).
	USERS="$(psql "$DATABASE_URL" -tAc "SELECT count(*) FROM users" 2>/dev/null | tr -dc 0-9)"
	if [ -z "$USERS" ]; then
		alert "DB row-count check failed (DB unreachable/not ready?) — skipping DB backup"
	elif [ "$USERS" -lt "$BACKUP_MIN_USERS" ]; then
		alert "DB looks EMPTY (users=$USERS < $BACKUP_MIN_USERS) — skipping DB backup; existing snapshots kept"
	elif pg_dump "$DATABASE_URL" --no-owner --no-privileges | gzip >"$TMP"; then
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

		# Empty-source guard: if the source bucket is empty but the backup still
		# holds objects, an rclone sync would MIRROR the emptiness and wipe the
		# backup. Skip + alert instead (auto-resumes when the source repopulates).
		SRC="$(rclone lsf -R "src:${b}" 2>/dev/null | grep -c .)"
		DST="$(rclone lsf -R "dest:${BACKUP_S3_BUCKET}/${BACKUP_FILES_PREFIX}${b}/" 2>/dev/null | grep -c .)"
		if [ "$SRC" -eq 0 ] && [ "$DST" -gt 0 ]; then
			alert "source bucket '${b}' is EMPTY but backup has ${DST} object(s) — skipping sync to protect the backup"
			continue
		fi

		# Optional --max-delete bounds a drastic partial shrink (off by default to
		# avoid false trips on normal bulk-expiry).
		if rclone sync "src:${b}" "dest:${BACKUP_S3_BUCKET}/${BACKUP_FILES_PREFIX}${b}/" \
			${BACKUP_MAX_DELETE:+--max-delete "$BACKUP_MAX_DELETE"} $EXCLUDE_ARGS; then
			log "files synced: ${b}"
		else
			log "FILE SYNC FAILED: ${b} (rclone error; possibly --max-delete=${BACKUP_MAX_DELETE} exceeded)"
		fi
	done

	sleep "$BACKUP_INTERVAL"
done
