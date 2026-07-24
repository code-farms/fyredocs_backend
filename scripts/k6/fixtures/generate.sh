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

# ODF fixtures (odt/ods/odp) can't be built with pure-Python stdlib — derive them
# from the docx/xlsx/pptx fixtures using LibreOffice inside the fyredocs-base
# image. Skipped (with a note) if Docker or the image isn't available; the k6
# suite then just skips the odt/ods/odp-to-pdf tools.
CAT="${1:-all}"
if [ "$CAT" = "all" ] || [ "$CAT" = "odf" ]; then
  if command -v docker >/dev/null 2>&1 && docker image inspect fyredocs-base:latest >/dev/null 2>&1; then
    echo "Generating ODF fixtures (odt/ods/odp) via LibreOffice…"
    docker run --rm -v "$DIR/out:/w" --entrypoint sh fyredocs-base:latest -c '
      mkdir -p /w/odt /w/ods /w/odp
      for s in small medium large; do
        [ -f /w/docx/$s.docx ] && soffice --headless -env:UserInstallation=file:///tmp/lo --convert-to odt --outdir /w/odt /w/docx/$s.docx >/dev/null 2>&1 || true
        [ -f /w/xlsx/$s.xlsx ] && soffice --headless -env:UserInstallation=file:///tmp/lo --convert-to ods --outdir /w/ods /w/xlsx/$s.xlsx >/dev/null 2>&1 || true
        [ -f /w/pptx/$s.pptx ] && soffice --headless -env:UserInstallation=file:///tmp/lo --convert-to odp --outdir /w/odp /w/pptx/$s.pptx >/dev/null 2>&1 || true
      done
    ' || echo "  (ODF generation failed — odt/ods/odp-to-pdf tools will be skipped)"
  else
    echo "Skipping ODF fixtures (need Docker + fyredocs-base image; run deploy.sh once)."
  fi
fi

echo ""
echo "Fixtures ready under $DIR/out"
echo "Tip: if the server rejects a synthetic office doc, replace it with a real"
echo "     file at the same path (e.g. out/docx/small.docx)."
