#!/usr/bin/env bash
# Stage 1 — format gate. Fails if any file needs gofmt.
source "$(dirname "${BASH_SOURCE[0]}")/00-lib.sh"

unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
  echo "gofmt needed on:"
  echo "$unformatted"
  exit 1
fi
echo "gofmt clean"
