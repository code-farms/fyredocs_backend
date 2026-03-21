#!/bin/sh
set -e

UNOSERVER_HOST="${UNOSERVER_HOST:-127.0.0.1}"
UNOSERVER_PORT="${UNOSERVER_PORT:-2002}"

# Start unoserver (persistent LibreOffice daemon) in background
echo "Starting unoserver on ${UNOSERVER_HOST}:${UNOSERVER_PORT}..."
unoserver --interface "$UNOSERVER_HOST" --port "$UNOSERVER_PORT" &
UNOSERVER_PID=$!

# Wait for unoserver to become ready (LibreOffice init takes ~2-5s)
READY=0
for i in $(seq 1 30); do
  if kill -0 "$UNOSERVER_PID" 2>/dev/null; then
    # Try a no-op conversion to verify the daemon is accepting connections
    if unoconvert --host "$UNOSERVER_HOST" --port "$UNOSERVER_PORT" /dev/null /dev/null 2>/dev/null; then
      READY=1
      break
    fi
  else
    echo "unoserver process died during startup"
    break
  fi
  sleep 1
done

if [ "$READY" -eq 1 ]; then
  echo "unoserver is ready (PID $UNOSERVER_PID)"
else
  echo "WARNING: unoserver did not become ready; conversions will use direct LibreOffice fallback"
fi

# Start the Go binary in background
./convert-to-pdf &
GO_PID=$!

# Forward termination signals to both processes
cleanup() {
  kill "$GO_PID" 2>/dev/null
  kill "$UNOSERVER_PID" 2>/dev/null
  wait "$GO_PID" 2>/dev/null
  wait "$UNOSERVER_PID" 2>/dev/null
  exit 0
}
trap cleanup SIGTERM SIGINT

# Wait for either process to exit
wait -n "$UNOSERVER_PID" "$GO_PID" 2>/dev/null
EXIT_CODE=$?

# If one exited, clean up the other
kill "$GO_PID" 2>/dev/null
kill "$UNOSERVER_PID" 2>/dev/null
wait "$GO_PID" 2>/dev/null
wait "$UNOSERVER_PID" 2>/dev/null

exit "$EXIT_CODE"
