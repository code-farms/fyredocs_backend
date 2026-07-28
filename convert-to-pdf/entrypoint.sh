#!/bin/sh
# Container entrypoint for convert-to-pdf.
#
# This service is two cooperating processes, which is why it needs an entrypoint
# rather than running the Go binary directly: a pool of unoserver (LibreOffice)
# daemons, plus the Go worker that drives them over the network. The daemons are
# started here, health-gated, and torn down together with the worker so a dead
# LibreOffice can never leave the container "up" but unable to convert.
#
# CONTRACT WITH THE GO SIDE (convert-to-pdf/processing/unopool.go): this script
# launches UNOSERVER_INSTANCES daemons on consecutive ports from UNOSERVER_PORT,
# and the Go pool independently recomputes that same port list. Nothing checks
# that the two agree.
#
# KNOWN DIVERGENCE in the defaults. When UNOSERVER_INSTANCES is unset or empty:
#   - this script falls back to 2
#   - unopool.go falls back to WORKER_CONCURRENCY + 1 (3 at the default
#     concurrency of 2)
# so the Go pool believes in a daemon on a port nothing is listening on. The
# symptom is not an error: conversions handed that port fail to connect and drop
# to the slow cold-start LibreOffice path, so the service just gets sporadically
# slower. deployment/deploy.sh avoids this by exporting a concrete
# UNOSERVER_INSTANCES (and deriving concurrency as instances - 1), but a plain
# `docker compose up` does not. Set UNOSERVER_INSTANCES explicitly, or keep the
# two fallbacks equal, if you change either.
set -e

UNOSERVER_HOST="${UNOSERVER_HOST:-127.0.0.1}"
UNOSERVER_PORT="${UNOSERVER_PORT:-2002}"
UNOSERVER_INSTANCES="${UNOSERVER_INSTANCES:-2}"

# Launch a pool of unoserver (LibreOffice) daemons on consecutive ports starting
# at UNOSERVER_PORT. Each daemon MUST get its own LibreOffice user profile
# (--user-installation) — sharing one profile causes a lock conflict and only
# the first instance starts. This must stay in sync with the Go pool, which
# derives its port list from UNOSERVER_PORT + UNOSERVER_INSTANCES.
UNOSERVER_PIDS=""
i=0
while [ "$i" -lt "$UNOSERVER_INSTANCES" ]; do
  port=$((UNOSERVER_PORT + i))
  profile="file:///tmp/uno-profile-${port}"
  echo "Starting unoserver on ${UNOSERVER_HOST}:${port} (profile ${profile})..."
  unoserver --interface "$UNOSERVER_HOST" --port "$port" --user-installation "$profile" &
  UNOSERVER_PIDS="$UNOSERVER_PIDS $!"
  i=$((i + 1))
done

# Wait for the first daemon to accept connections (LibreOffice init takes ~2-5s).
# The remaining daemons start in parallel; the Go side falls back to direct
# LibreOffice for any daemon that is not yet ready, so we only gate on one.
FIRST_PORT="$UNOSERVER_PORT"
FIRST_PID=$(echo "$UNOSERVER_PIDS" | awk '{print $1}')
READY=0
for _ in $(seq 1 30); do
  if kill -0 "$FIRST_PID" 2>/dev/null; then
    if unoconvert --host "$UNOSERVER_HOST" --port "$FIRST_PORT" /dev/null /dev/null 2>/dev/null; then
      READY=1
      break
    fi
  else
    echo "first unoserver process died during startup"
    break
  fi
  sleep 1
done

if [ "$READY" -eq 1 ]; then
  echo "unoserver pool ready ($UNOSERVER_INSTANCES instance(s) from port $UNOSERVER_PORT)"
else
  echo "WARNING: unoserver did not become ready; conversions will use direct LibreOffice fallback"
fi

# Start the Go binary in background
./convert-to-pdf &
GO_PID=$!

# Forward termination signals to the Go binary and every unoserver daemon.
cleanup() {
  kill "$GO_PID" 2>/dev/null
  for pid in $UNOSERVER_PIDS; do kill "$pid" 2>/dev/null; done
  wait "$GO_PID" 2>/dev/null
  for pid in $UNOSERVER_PIDS; do wait "$pid" 2>/dev/null; done
  exit 0
}
trap cleanup SIGTERM SIGINT

# Wait for any managed process to exit, then tear the rest down.
wait -n "$GO_PID" $UNOSERVER_PIDS 2>/dev/null
EXIT_CODE=$?

kill "$GO_PID" 2>/dev/null
for pid in $UNOSERVER_PIDS; do kill "$pid" 2>/dev/null; done
wait "$GO_PID" 2>/dev/null
for pid in $UNOSERVER_PIDS; do wait "$pid" 2>/dev/null; done

exit "$EXIT_CODE"
