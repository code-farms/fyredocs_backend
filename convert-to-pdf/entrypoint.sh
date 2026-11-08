#!/bin/sh
# Container entrypoint for convert-to-pdf.
#
# By default the Go worker supervises its own unoserver (LibreOffice) daemons —
# start, restart, recycle, stop — in convert-to-pdf/processing/daemons.go, so
# this script only hands over to it with exec. Owning the daemons in Go removed
# a duplicated port list, a PID-file contract and a class of untestable shell
# lifecycle bugs (docs/developer/services/convert-to-pdf.md, "Daemon lifecycle").
#
# ROLLBACK, ONE RELEASE ONLY: UNOSERVER_SUPERVISOR=shell runs the previous
# shell supervisor below instead, and the Go worker then leaves the daemons
# alone. NOTE: the Go side reads the same variable (GoSupervisionEnabled).
# Delete everything after the exec, the flag, and recycleShellDaemon /
# daemonPIDFile in processing/processing_free.go once the Go supervisor has run
# a release in production.
set -e

if [ "${UNOSERVER_SUPERVISOR:-go}" != "shell" ]; then
  exec ./convert-to-pdf
fi

# ─── Shell supervision (rollback path) ───
#
# CONTRACT WITH THE GO SIDE (convert-to-pdf/processing/unopool.go): this script
# launches UNOSERVER_INSTANCES daemons on consecutive ports from UNOSERVER_PORT,
# and the Go pool independently recomputes that same port list. When
# UNOSERVER_INSTANCES is unset this script falls back to 2 while unopool.go
# falls back to WORKER_CONCURRENCY + 1, so jobs handed the missing port take the
# cold path. deployment/deploy.sh sets UNOSERVER_INSTANCES explicitly.

UNOSERVER_HOST="${UNOSERVER_HOST:-127.0.0.1}"
UNOSERVER_PORT="${UNOSERVER_PORT:-2002}"
UNOSERVER_INSTANCES="${UNOSERVER_INSTANCES:-2}"
# Offset from each daemon's XML-RPC port to the port its LibreOffice listens on
# for UNO. Must be >= UNOSERVER_INSTANCES so no UNO port lands on another
# daemon's XML-RPC port.
UNOSERVER_UNO_PORT_OFFSET="${UNOSERVER_UNO_PORT_OFFSET:-100}"

# Launch a pool of unoserver (LibreOffice) daemons on consecutive ports starting
# at UNOSERVER_PORT. Each daemon MUST get its own LibreOffice user profile
# (--user-installation) — sharing one profile causes a lock conflict and only
# the first instance starts. This must stay in sync with the Go pool, which
# derives its port list from UNOSERVER_PORT + UNOSERVER_INSTANCES.
#
# Two flags here are load-bearing, and getting either wrong kills every daemon
# while the container stays "up" on the cold fallback (finding D23/D23b —
# docs/developer/architecture/next-generation-document-engine/01-existing-system-audit.md):
#   --user-installation takes a filesystem PATH. unoserver converts it to a URI
#     itself (Path(arg).as_uri()); passing "file:///…" raises ValueError at start.
#   --uno-port defaults to 2002 in unoserver 3.x and must differ from --port, so
#     the daemon on UNOSERVER_PORT=2002 refuses to start unless it is set.
#
# Daemon-side backstop for a conversion that never finishes: unoserver kills its
# LibreOffice and exits. The Go worker normally gives up much earlier with a
# size-scaled deadline (unoconvertTimeout in processing/processing_free.go);
# this only catches what the worker missed. Default = the 10-minute job context.
UNOSERVER_CONVERSION_TIMEOUT="${UNOSERVER_CONVERSION_TIMEOUT:-600}"

# supervise PORT keeps one unoserver daemon running on PORT, restarting it
# whenever it exits. Daemons exit by design — the Go worker kills a daemon's
# LibreOffice when a conversion overruns or its job is cancelled (so the daemon
# never keeps working on an abandoned file), and --conversion-timeout does the
# same daemon-side — and by accident when LibreOffice crashes. Restarting one
# daemon is the right response to all three; tearing the container down (the
# previous behaviour) killed every in-flight job with it.
#
# NOTE: the PID-file path /tmp/uno-lo-<port>.pid is a contract with
# daemonPIDFile in convert-to-pdf/processing/processing_free.go, which reads it
# to kill a stuck daemon's LibreOffice. Change both together.
#
# Backoff grows by a second per consecutive quick failure (capped at 30 s) and
# resets once a daemon has stayed up for a minute, so a misconfiguration does
# not spin the CPU and a one-off crash restarts almost immediately.
#
# Each daemon runs under setsid so it leads its own process group, and stopping
# it signals the whole group: TERM, then KILL after 2 s. Signalling unoserver
# alone is not enough — on TERM it forwards to its LibreOffice launcher
# (oosplash), soffice.bin exits, but oosplash lingers and unoserver waits on it
# forever, so `docker stop` hung until the grace period ran out and SIGKILLed
# the container (measured 2026-11-08). 2 s is enough because daemons are only
# stopped after the Go worker has drained, so no conversion is in flight.
# busybox setsid does not fork from a background job, so the child PID is also
# the group ID.
#
# No `wait` after the KILL: a wait inside this TERM trap made busybox ash end
# with status 137 instead of reaching `exit 0` (measured 2026-11-08). Anything
# left after SIGKILL is a zombie, which docker-init reaps once this exits.
stop_daemon() {
  [ -n "$child" ] || return 0
  kill -TERM "-$child" 2>/dev/null
  n=0
  while kill -0 "$child" 2>/dev/null && [ "$n" -lt 20 ]; do
    sleep 0.1
    n=$((n + 1))
  done
  kill -KILL "-$child" 2>/dev/null
  child=""
}

supervise() {
  port="$1"
  uno_port=$((port + UNOSERVER_UNO_PORT_OFFSET))
  profile="/tmp/uno-profile-${port}"
  child=""
  trap 'stop_daemon; exit 0' TERM INT
  failures=0
  while :; do
    echo "Starting unoserver on ${UNOSERVER_HOST}:${port} (uno-port ${uno_port}, profile ${profile})..."
    started=$(date +%s)
    setsid unoserver --interface "$UNOSERVER_HOST" --port "$port" --uno-port "$uno_port" \
      --user-installation "$profile" \
      --libreoffice-pid-file "/tmp/uno-lo-${port}.pid" \
      --conversion-timeout "$UNOSERVER_CONVERSION_TIMEOUT" &
    child=$!
    code=0
    wait "$child" || code=$?
    # Sweep the group before restarting. When unoserver exits — LibreOffice
    # crashed, --conversion-timeout fired, or the worker recycled it — an orphaned
    # soffice.bin can survive in the group, still holding the profile lock and
    # UNO port, and the restarted daemon's LibreOffice then exits immediately.
    kill -KILL "-$child" 2>/dev/null || true
    child=""
    if [ "$code" -eq 137 ]; then
      # SIGKILL: a deliberate recycle by the Go worker (recycleDaemon), not a
      # crash. Restart at once; counting these as failures would back a busy
      # daemon off towards 30 s and push jobs onto the cold path.
      failures=0
      delay=0
    else
      if [ $(( $(date +%s) - started )) -ge 60 ]; then failures=0; fi
      failures=$((failures + 1))
      delay=$failures
      if [ "$delay" -gt 30 ]; then delay=30; fi
    fi
    echo "unoserver on port ${port} exited (code ${code}); restarting in ${delay}s"
    sleep "$delay"
  done
}

SUPERVISOR_PIDS=""
i=0
while [ "$i" -lt "$UNOSERVER_INSTANCES" ]; do
  supervise $((UNOSERVER_PORT + i)) &
  SUPERVISOR_PIDS="$SUPERVISOR_PIDS $!"
  i=$((i + 1))
done

# Wait for the first daemon to accept connections (LibreOffice init takes ~2-5s).
# The remaining daemons start in parallel; the Go side falls back to direct
# LibreOffice for any daemon that is not yet ready, so we only gate on one.
#
# unoping is the probe because it asks the daemon itself. The previous probe
# converted /dev/null, which LibreOffice rejects ("source file could not be
# loaded") even when the daemon is healthy, so it could never report ready.
FIRST_PORT="$UNOSERVER_PORT"
READY=0
for _ in $(seq 1 30); do
  if unoping --host "$UNOSERVER_HOST" --port "$FIRST_PORT" >/dev/null 2>&1; then
    READY=1
    break
  fi
  sleep 1
done

if [ "$READY" -eq 1 ]; then
  echo "unoserver pool ready ($UNOSERVER_INSTANCES instance(s) from port $UNOSERVER_PORT)"
else
  # The worker still starts so jobs complete on the cold path, but /readyz now
  # reports 503 while no daemon is up (convert-to-pdf/main.go) and every cold
  # conversion increments convert_fallback_total, so this state is visible.
  echo "WARNING: unoserver did not become ready; conversions will use direct LibreOffice fallback"
fi

# Start the Go binary in background
./convert-to-pdf &
GO_PID=$!

# On TERM: let the Go worker drain first (it finishes or NAKs in-flight jobs,
# WORKER_DRAIN_TIMEOUT), and only then stop the daemons. Stopping them first
# would push every in-flight Office job onto the cold LibreOffice path for its
# last seconds. Each supervisor stops its own daemon's process group on TERM.
cleanup() {
  kill "$GO_PID" 2>/dev/null
  wait "$GO_PID" 2>/dev/null
  for pid in $SUPERVISOR_PIDS; do kill "$pid" 2>/dev/null; done
  for pid in $SUPERVISOR_PIDS; do wait "$pid" 2>/dev/null; done
  exit 0
}
trap cleanup SIGTERM SIGINT

# Only the Go worker exiting ends the container: daemons are restarted by their
# supervisors, so a LibreOffice crash no longer takes in-flight jobs down too.
EXIT_CODE=0
wait "$GO_PID" 2>/dev/null || EXIT_CODE=$?

for pid in $SUPERVISOR_PIDS; do kill "$pid" 2>/dev/null; done
for pid in $SUPERVISOR_PIDS; do wait "$pid" 2>/dev/null; done

exit "$EXIT_CODE"
