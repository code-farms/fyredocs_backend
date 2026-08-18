#!/usr/bin/env bash
# Stage 7 — every module must compile, tests included (vet compiles packages
# but not test binaries for other modules' consumers).
source "$(dirname "${BASH_SOURCE[0]}")/00-lib.sh"

for_each_module go build
