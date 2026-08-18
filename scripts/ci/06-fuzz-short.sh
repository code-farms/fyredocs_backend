#!/usr/bin/env bash
# Stage 6 — smoke fuzzing: every Fuzz* target for FUZZTIME (default 10s).
# Long fuzz sessions stay out of the pipeline by design.
source "$(dirname "${BASH_SOURCE[0]}")/00-lib.sh"

sh scripts/fuzz-short.sh
