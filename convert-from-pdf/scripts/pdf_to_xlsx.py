#!/usr/bin/env python3
"""Convert a PDF to XLSX by extracting tables (pdfplumber) into sheets.

Usage: pdf_to_xlsx.py <input.pdf> <output.xlsx>

Real tables become one sheet per table; pages without a detected table fall
back to a text dump (one row per line) so non-tabular PDFs still yield a valid,
non-empty workbook. Always writes a valid .xlsx (an empty sheet if the PDF has
no extractable content) so a job never fails merely because a PDF had no tables.
PDF->spreadsheet is inherently lossy; this gives real tables when present and a
readable text fallback otherwise.
"""
import sys

import pdfplumber
from openpyxl import Workbook


def _safe_title(base: str, used: set) -> str:
    # Excel sheet titles: <=31 chars, and must be unique.
    title = (base or "Sheet")[:31]
    n = 1
    while title in used:
        suffix = f"_{n}"
        title = (base[: 31 - len(suffix)] + suffix) if base else f"Sheet{suffix}"
        n += 1
    used.add(title)
    return title


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: pdf_to_xlsx.py <input.pdf> <output.xlsx>", file=sys.stderr)
        return 2
    src, dst = sys.argv[1], sys.argv[2]

    wb = Workbook()
    wb.remove(wb.active)  # start empty; add sheets as we find content
    used_titles: set = set()
    wrote_any = False

    with pdfplumber.open(src) as pdf:
        for page_index, page in enumerate(pdf.pages, start=1):
            tables = page.extract_tables() or []
            if tables:
                for t_index, table in enumerate(tables, start=1):
                    ws = wb.create_sheet(_safe_title(f"P{page_index}_T{t_index}", used_titles))
                    for row in table:
                        ws.append(["" if cell is None else str(cell) for cell in row])
                    wrote_any = True
            else:
                text = page.extract_text() or ""
                if text.strip():
                    ws = wb.create_sheet(_safe_title(f"Page{page_index}", used_titles))
                    for line in text.splitlines():
                        ws.append([line])
                    wrote_any = True

    if not wrote_any:
        wb.create_sheet(_safe_title("Sheet1", used_titles))

    wb.save(dst)
    return 0


if __name__ == "__main__":
    sys.exit(main())
