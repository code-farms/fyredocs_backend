#!/usr/bin/env bash
# check-compose-hardening.sh — fails when any Fyredocs service, in any compose
# variant we deploy, renders without the container hardening from the
# x-hardened anchor in deployment/docker-compose.yml: read_only, cap_drop ALL,
# no-new-privileges and uid 10001.
#
# The per-service slices and the multi-host app file inherit the hardening via
# `extends`, so a change there (or an edited anchor) can silently drop it; this
# renders every variant with `docker compose config` (no daemon needed) and
# checks the result. Needs the docker CLI with the compose plugin, and python3.
#
# Usage: scripts/ci/check-compose-hardening.sh   (from anywhere; CI-friendly)
source "$(dirname "${BASH_SOURCE[0]}")/00-lib.sh"

# NOTE: the service list mirrors SERVICES in 00-lib.sh minus `shared`, which is
# a library, not a container.
HARDENED=(api-gateway auth-service job-service convert-to-pdf convert-from-pdf
  organize-pdf optimize-pdf analytics-service document-service user-service
  notification-service ai-service)

ENV_FILE=.env
[[ -f "$ENV_FILE" ]] || ENV_FILE=.env.example

# render <compose file> — prints the rendered config as JSON with every
# profile enabled. Missing variables only produce warnings, which are dropped.
render() {
  docker compose -f "$1" --env-file "$ENV_FILE" --profile '*' config --format json 2>/dev/null
}

# PY_CHECK reads one rendered config (JSON on stdin) and reports every
# hardened service missing a key. argv: the compose file name, then the
# service names to check.
read -r -d '' PY_CHECK <<'PY' || true
import json, sys
file, wanted = sys.argv[1], set(sys.argv[2:])
services = json.load(sys.stdin).get("services", {})
present = sorted(wanted & services.keys())
fail = False
for name in present:
    s = services[name]
    problems = []
    if s.get("read_only") is not True:
        problems.append("read_only")
    if s.get("cap_drop") != ["ALL"]:
        problems.append("cap_drop ALL")
    if "no-new-privileges:true" not in (s.get("security_opt") or []):
        problems.append("no-new-privileges")
    if s.get("user") != "10001:10001":
        problems.append("user 10001:10001")
    if problems:
        fail = True
        print(f"FAIL {file}: {name} is missing {', '.join(problems)}")
if not fail:
    print(f"ok   {file}: {len(present)} services checked")
sys.exit(1 if fail else 0)
PY

check() {
  local file="$1" json
  if ! json="$(render "$file")"; then
    echo "FAIL $file: docker compose config did not render"
    return 1
  fi
  python3 -c "$PY_CHECK" "$file" "${HARDENED[@]}" <<<"$json"
}

fail=0
for f in deployment/docker-compose.yml deployment/docker-compose.app.yml deployment/docker-compose-*.yml; do
  check "$f" || fail=1
done
exit $fail
