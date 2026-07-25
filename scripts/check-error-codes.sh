#!/usr/bin/env bash
# check-error-codes.sh — fails when a handler uses a bare error-code literal
# that has a canonical constant in shared/response/codes.go (audit M14).
#
# Handlers must pass response.Code* constants, never the raw strings, so the
# API speaks one error vocabulary. Domain-specific codes (INVALID_CREDENTIALS,
# USER_ALREADY_EXISTS, ...) are allowed — only the canonicalized set is banned.
#
# Usage: ./scripts/check-error-codes.sh   (run from the repo root; CI-friendly)
set -euo pipefail

cd "$(dirname "$0")/.."

BANNED='"(INVALID_BODY|INVALID_REQUEST|INVALID_INPUT|INVALID_ID|UNAUTHORIZED|FORBIDDEN|NOT_FOUND|SERVER_ERROR|LIST_FAILED|UPDATE_FAILED|CREATE_FAILED)"'

# Scan handler/route/httpapi code in every service; skip tests (they may assert
# on wire values) and shared/response (it defines the constants).
matches=$(grep -rnE "$BANNED" \
    --include='*.go' \
    --exclude='*_test.go' \
    . 2>/dev/null | grep -v '^\./shared/response/' || true)

if [[ -n "$matches" ]]; then
    echo "ERROR: bare error-code literals found — use the response.Code* constants instead:" >&2
    echo "$matches" >&2
    exit 1
fi

echo "OK: no bare error-code literals."
