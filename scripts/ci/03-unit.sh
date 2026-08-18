#!/usr/bin/env bash
# Stage 3 — unit tests, race-checked and shuffled, fully offline.
source "$(dirname "${BASH_SOURCE[0]}")/00-lib.sh"

for_each_module go test -race -count=1 -shuffle=on -timeout=10m
