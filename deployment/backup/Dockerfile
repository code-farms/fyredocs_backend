# Postgres logical-backup sidecar: pg_dump (matching the PG18 server) + rclone
# (provider-neutral S3 client for Cloudflare R2 / Backblaze B2 / S3 / MinIO).
# No AWS account or aws-cli involved — rclone speaks the S3 API to any endpoint.
FROM postgres:18-alpine

RUN apk add --no-cache rclone curl

COPY deployment/backup/backup.sh /usr/local/bin/backup.sh
RUN chmod +x /usr/local/bin/backup.sh

# HEALTHCHECK marks the container unhealthy if no backup has succeeded within
# ~2 intervals, so a stalled/failing backup is visible in `docker ps`.
HEALTHCHECK --interval=60s --timeout=5s --start-period=120s --retries=2 \
	CMD sh -c 'test -f /tmp/last_success && \
		[ $(( $(date +%s) - $(stat -c %Y /tmp/last_success) )) -lt $(( ${BACKUP_INTERVAL:-3600} * 2 + 120 )) ]'

ENTRYPOINT ["/usr/local/bin/backup.sh"]
