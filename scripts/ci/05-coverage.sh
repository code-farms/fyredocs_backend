#!/usr/bin/env bash
# Stage 5 — coverage profiles (unit tier). Emits coverage/<svc>.out and the
# merged coverage/merged.out; the merged func summary is the stage's artifact.
source "$(dirname "${BASH_SOURCE[0]}")/00-lib.sh"

sh scripts/coverage.sh
