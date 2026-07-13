#!/usr/bin/env sh
# Guards the deployment invariant:
#
#   Only the Caddy edge publishes host ports (80/443). Everything else is either
#   internal to the Docker network or bound to 127.0.0.1 (loopback-only).
#
# This catches an accidental `ports:` mapping (e.g. copying a service block and
# leaving a "8081:8081") that would unintentionally expose an internal /
# incremental service port to the host — or the internet, since Docker's
# published ports bypass host firewalls like UFW.
#
# Pure text scan of the compose files — does NOT require the Docker daemon.
# Run from the repo root (fyredocs_backend/), or via `make check-ports`.
set -eu

violations=$(mktemp)
trap 'rm -f "$violations"' EXIT

for f in deployment/docker-compose*.yml; do
	[ -f "$f" ] || continue

	# Quoted port mappings that appear as list items under a `ports:` key, e.g.:
	#   - "80:80"  |  - "127.0.0.1:9000:9000"  |  - "8081:8081"
	grep -nE '^[[:space:]]*-[[:space:]]*"[0-9.]+:[0-9]+(:[0-9]+)?"' "$f" | \
	while IFS= read -r hit; do
		lineno=$(printf '%s' "$hit" | cut -d: -f1)
		mapping=$(printf '%s' "$hit" | sed -E 's/.*"([^"]+)".*/\1/')

		case "$mapping" in
			80:80|443:443) ;;   # Caddy public edge — the only allowed host ports
			127.0.0.1:*)   ;;   # loopback-only — safe for local dev tooling
			*)
				printf 'FAIL %s:%s  host-published port "%s" -> bind to 127.0.0.1 or remove\n' \
					"$f" "$lineno" "$mapping" >> "$violations" ;;
		esac
	done
done

if [ -s "$violations" ]; then
	cat "$violations"
	exit 1
fi

echo "OK: only Caddy (80/443) and 127.0.0.1-bound ports are host-published."
