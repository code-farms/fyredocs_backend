#!/usr/bin/env bash
# Generate synthetic load-test fixtures (pure Python stdlib — no LibreOffice/gs/
# ImageMagick needed). Output: fixtures/out/<category>/<size>.<ext>
#
# Usage:  ./generate.sh [category]
#   category: all (default) | pdf | scanned-pdf | docx | xlsx | pptx | image | html
#
# To use your OWN representative files instead of synthetic ones, drop them at
# fixtures/out/<category>/{small,medium,large}.<ext> — the k6 suite picks up
# whatever is present.
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to generate fixtures." >&2
  exit 1
fi

python3 "$DIR/gen.py" "${1:-all}"

echo ""
echo "Fixtures ready under $DIR/out"
echo "Tip: if the server rejects a synthetic office doc, replace it with a real"
echo "     file at the same path (e.g. out/docx/small.docx)."
