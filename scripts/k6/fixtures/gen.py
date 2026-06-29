#!/usr/bin/env python3
"""Generate synthetic, valid load-test fixtures using only the Python stdlib.

No LibreOffice / Ghostscript / ImageMagick needed — OOXML is zip+XML, PDFs and
PNGs are assembled from raw bytes. Output: fixtures/out/<category>/<size>.<ext>

Categories: pdf, scanned-pdf, docx, xlsx, pptx, image(png), html
Sizes:      small (~0.8MB), medium (~8MB), large (~40MB)

These are good enough to exercise every tool's pipeline under load. If a server
tool rejects a synthetic office doc, drop a real file at the same path — the k6
suite picks up whatever is present.
"""
import os
import sys
import zlib
import struct

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "out")

# approximate target sizes in bytes
TARGETS = {"small": 800_000, "medium": 8_000_000, "large": 40_000_000}


def write(cat, size, ext, data):
    d = os.path.join(OUT, cat)
    os.makedirs(d, exist_ok=True)
    p = os.path.join(d, f"{size}.{ext}")
    with open(p, "wb") as f:
        f.write(data)
    print(f"  {cat}/{size}.{ext}  {len(data)//1024} KB")


# --------------------------------------------------------------------------
# PDF (vector text pages + optional padding to hit target size)
# --------------------------------------------------------------------------
def pdf_text(n_pages, pad_to=0):
    objs = []  # list of byte strings, object n = index n-1 (1-based ids)

    def add(s):
        objs.append(s if isinstance(s, bytes) else s.encode("latin-1"))
        return len(objs)  # object id

    catalog_id = add("")  # placeholder, fill later (id 1)
    pages_id = add("")    # id 2
    font_id = add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

    kids = []
    for i in range(n_pages):
        lines = [f"Fyredocs load-test page {i+1} of {n_pages}."]
        lines += [f"Line {j}: the quick brown fox jumps over the lazy dog 0123456789." for j in range(40)]
        content = "BT /F1 12 Tf 50 740 Td 14 TL\n"
        content += "".join(f"({ln}) Tj T*\n" for ln in lines)
        content += "ET\n"
        # pad the first page's stream with an ignored comment to reach pad_to
        if i == 0 and pad_to:
            content += "% PAD "  # filler follows; PDF comments run to EOL
        stream = content
        if i == 0 and pad_to:
            # rough filler; corrected after we know overhead
            stream += "A" * 1
        cid = add(f"<< /Length {len(stream)} >>\nstream\n{stream}\nendstream")
        pid = add(
            f"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
            f"/Resources << /Font << /F1 {font_id} 0 R >> >> /Contents {cid} 0 R >>"
        )
        kids.append(pid)

    objs[catalog_id - 1] = b"<< /Type /Catalog /Pages 2 0 R >>"
    kid_refs = " ".join(f"{k} 0 R" for k in kids)
    objs[pages_id - 1] = f"<< /Type /Pages /Kids [{kid_refs}] /Count {n_pages} >>".encode("latin-1")

    def serialize(objects):
        out = b"%PDF-1.5\n%\xe2\xe3\xcf\xd3\n"
        offsets = []
        for idx, body in enumerate(objects, start=1):
            offsets.append(len(out))
            out += f"{idx} 0 obj\n".encode("latin-1") + body + b"\nendobj\n"
        xref_pos = len(out)
        out += f"xref\n0 {len(objects)+1}\n".encode("latin-1")
        out += b"0000000000 65535 f \n"
        for off in offsets:
            out += f"{off:010d} 00000 n \n".encode("latin-1")
        out += (
            f"trailer\n<< /Size {len(objects)+1} /Root 1 0 R >>\n"
            f"startxref\n{xref_pos}\n%%EOF\n"
        ).encode("latin-1")
        return out

    data = serialize(objs)
    if pad_to and len(data) < pad_to:
        # extend the first content stream's filler, then re-serialize once
        need = pad_to - len(data)
        # rebuild objs with the right filler length on the first content stream
        for i, body in enumerate(objs):
            if body.startswith(b"<< /Length "):
                # this is the first content object
                base = body.split(b"stream\n", 1)[1].rsplit(b"\nendstream", 1)[0]
                filler = b"A" * (need + 1)
                newstream = base[:-1] + filler  # replace the single 'A'
                objs[i] = f"<< /Length {len(newstream)} >>\nstream\n".encode("latin-1") + newstream + b"\nendstream"
                break
        data = serialize(objs)
    return data


# --------------------------------------------------------------------------
# PNG (RGB or gray, raw scanlines + zlib) and image-PDF for OCR
# --------------------------------------------------------------------------
def png_bytes(w, h, gray=False, noisy=True):
    chans = 1 if gray else 3
    raw = bytearray()
    row = os.urandom(w * chans) if noisy else bytes(w * chans)
    for _ in range(h):
        raw.append(0)  # filter type 0
        raw += (os.urandom(w * chans) if noisy else row)
    comp = zlib.compress(bytes(raw), 6)

    def chunk(typ, data):
        c = typ + data
        return struct.pack(">I", len(data)) + c + struct.pack(">I", zlib.crc32(c) & 0xFFFFFFFF)

    sig = b"\x89PNG\r\n\x1a\n"
    color = 0 if gray else 2
    ihdr = struct.pack(">IIBBBBB", w, h, 8, color, 0, 0, 0)
    return sig + chunk(b"IHDR", ihdr) + chunk(b"IDAT", comp) + chunk(b"IEND", b"")


def pdf_image(n_pages, w, h):
    """PDF whose pages are a single grayscale image — no text layer (for OCR)."""
    objs = []

    def add(s):
        objs.append(s if isinstance(s, bytes) else s.encode("latin-1"))
        return len(objs)

    add("")  # catalog id 1
    add("")  # pages id 2

    # one shared noisy grayscale image (FlateDecode raw pixels)
    pix = os.urandom(w * h)
    comp = zlib.compress(pix, 6)
    img_id = add(
        f"<< /Type /XObject /Subtype /Image /Width {w} /Height {h} "
        f"/ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode "
        f"/Length {len(comp)} >>\nstream\n".encode("latin-1") + comp + b"\nendstream"
    )

    kids = []
    for _ in range(n_pages):
        content = f"q {w} 0 0 {h} 0 0 cm /Im0 Do Q".encode("latin-1")
        cid = add(b"<< /Length %d >>\nstream\n" % len(content) + content + b"\nendstream")
        pid = add(
            f"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 {w} {h}] "
            f"/Resources << /XObject << /Im0 {img_id} 0 R >> >> /Contents {cid} 0 R >>"
        )
        kids.append(pid)

    objs[0] = b"<< /Type /Catalog /Pages 2 0 R >>"
    kid_refs = " ".join(f"{k} 0 R" for k in kids)
    objs[1] = f"<< /Type /Pages /Kids [{kid_refs}] /Count {n_pages} >>".encode("latin-1")

    out = b"%PDF-1.5\n%\xe2\xe3\xcf\xd3\n"
    offs = []
    for idx, body in enumerate(objs, start=1):
        offs.append(len(out))
        out += f"{idx} 0 obj\n".encode("latin-1") + body + b"\nendobj\n"
    xref = len(out)
    out += f"xref\n0 {len(objs)+1}\n".encode("latin-1") + b"0000000000 65535 f \n"
    for o in offs:
        out += f"{o:010d} 00000 n \n".encode("latin-1")
    out += f"trailer\n<< /Size {len(objs)+1} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n".encode("latin-1")
    return out


# --------------------------------------------------------------------------
# OOXML (docx / xlsx / pptx) via zipfile — pad with a stored orphan part
# --------------------------------------------------------------------------
import zipfile
import io


def zip_parts(parts, pad_to=0):
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as z:
        for name, data in parts.items():
            z.writestr(name, data)
        if pad_to:
            cur = buf.tell()  # not exact pre-close, approximated below
        # add incompressible filler stored (size counts toward file)
        if pad_to:
            need = pad_to - len(buf.getvalue())
            if need > 0:
                z.writestr(zipfile.ZipInfo("docProps/filler.bin"), os.urandom(need), zipfile.ZIP_STORED)
    return buf.getvalue()


def docx(pad_to=0):
    paras = "".join(f"<w:p><w:r><w:t>Fyredocs load-test paragraph {i}.</w:t></w:r></w:p>" for i in range(30))
    parts = {
        "[Content_Types].xml":
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
            '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
            '<Default Extension="xml" ContentType="application/xml"/>'
            '<Default Extension="bin" ContentType="application/octet-stream"/>'
            '<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>'
            '</Types>',
        "_rels/.rels":
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
            '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>'
            '</Relationships>',
        "word/document.xml":
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>'
            + paras + '<w:sectPr/></w:body></w:document>',
    }
    return zip_parts(parts, pad_to)


def xlsx(pad_to=0):
    rows = "".join(
        "<row r=\"%d\">%s</row>" % (
            r, "".join(f"<c t=\"inlineStr\"><is><t>R{r}C{c}</t></is></c>" for c in range(1, 6))
        ) for r in range(1, 51)
    )
    parts = {
        "[Content_Types].xml":
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
            '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
            '<Default Extension="xml" ContentType="application/xml"/>'
            '<Default Extension="bin" ContentType="application/octet-stream"/>'
            '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>'
            '<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>'
            '</Types>',
        "_rels/.rels":
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
            '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>'
            '</Relationships>',
        "xl/workbook.xml":
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" '
            'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">'
            '<sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>',
        "xl/_rels/workbook.xml.rels":
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
            '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>'
            '</Relationships>',
        "xl/worksheets/sheet1.xml":
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">'
            '<sheetData>' + rows + '</sheetData></worksheet>',
    }
    return zip_parts(parts, pad_to)


def pptx(pad_to=0):
    # Minimal one-slide deck (presentation + master + layout + slide + theme).
    ns_p = "http://schemas.openxmlformats.org/presentationml/2006/main"
    ns_r = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
    ns_a = "http://schemas.openxmlformats.org/drawingml/2006/main"
    parts = {
        "[Content_Types].xml":
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
            '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
            '<Default Extension="xml" ContentType="application/xml"/>'
            '<Default Extension="bin" ContentType="application/octet-stream"/>'
            '<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>'
            '<Override PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>'
            '<Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>'
            '<Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>'
            '<Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>'
            '</Types>',
        "_rels/.rels":
            f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            f'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
            f'<Relationship Id="rId1" Type="{ns_r}/officeDocument" Target="ppt/presentation.xml"/></Relationships>',
        "ppt/presentation.xml":
            f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            f'<p:presentation xmlns:p="{ns_p}" xmlns:r="{ns_r}">'
            f'<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>'
            f'<p:sldIdLst><p:sldId id="256" r:id="rId2"/></p:sldIdLst>'
            f'<p:sldSz cx="9144000" cy="6858000"/></p:presentation>',
        "ppt/_rels/presentation.xml.rels":
            f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            f'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
            f'<Relationship Id="rId1" Type="{ns_r}/slideMaster" Target="slideMasters/slideMaster1.xml"/>'
            f'<Relationship Id="rId2" Type="{ns_r}/slide" Target="slides/slide1.xml"/>'
            f'<Relationship Id="rId3" Type="{ns_r}/theme" Target="theme/theme1.xml"/></Relationships>',
        "ppt/slides/slide1.xml":
            f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            f'<p:sld xmlns:p="{ns_p}" xmlns:a="{ns_a}"><p:cSld><p:spTree>'
            f'<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>'
            f'<p:grpSpPr/></p:spTree></p:cSld><p:clrMapOvr><a:overrideClrMapping/></p:clrMapOvr></p:sld>',
        "ppt/slides/_rels/slide1.xml.rels":
            f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            f'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
            f'<Relationship Id="rId1" Type="{ns_r}/slideLayout" Target="../slideLayouts/slideLayout1.xml"/></Relationships>',
        "ppt/slideLayouts/slideLayout1.xml":
            f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            f'<p:sldLayout xmlns:p="{ns_p}" xmlns:a="{ns_a}" type="blank"><p:cSld name="Blank"><p:spTree>'
            f'<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>'
            f'</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>',
        "ppt/slideLayouts/_rels/slideLayout1.xml.rels":
            f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            f'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
            f'<Relationship Id="rId1" Type="{ns_r}/slideMaster" Target="../slideMasters/slideMaster1.xml"/></Relationships>',
        "ppt/slideMasters/slideMaster1.xml":
            f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            f'<p:sldMaster xmlns:p="{ns_p}" xmlns:a="{ns_a}"><p:cSld><p:spTree>'
            f'<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>'
            f'</p:spTree></p:cSld><p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" '
            f'accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" '
            f'hlink="hlink" folHlink="folHlink"/>'
            f'<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1" xmlns:r="{ns_r}"/></p:sldLayoutIdLst>'
            f'</p:sldMaster>',
        "ppt/slideMasters/_rels/slideMaster1.xml.rels":
            f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            f'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
            f'<Relationship Id="rId1" Type="{ns_r}/slideLayout" Target="../slideLayouts/slideLayout1.xml"/></Relationships>',
        "ppt/theme/theme1.xml":
            f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            f'<a:theme xmlns:a="{ns_a}" name="Office"><a:themeElements>'
            f'<a:clrScheme name="Office"><a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1>'
            f'<a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>'
            f'<a:dk2><a:srgbClr val="1F497D"/></a:dk2><a:lt2><a:srgbClr val="EEECE1"/></a:lt2>'
            f'<a:accent1><a:srgbClr val="4F81BD"/></a:accent1><a:accent2><a:srgbClr val="C0504D"/></a:accent2>'
            f'<a:accent3><a:srgbClr val="9BBB59"/></a:accent3><a:accent4><a:srgbClr val="8064A2"/></a:accent4>'
            f'<a:accent5><a:srgbClr val="4BACC6"/></a:accent5><a:accent6><a:srgbClr val="F79646"/></a:accent6>'
            f'<a:hlink><a:srgbClr val="0000FF"/></a:hlink><a:folHlink><a:srgbClr val="800080"/></a:folHlink></a:clrScheme>'
            f'<a:fontScheme name="Office"><a:majorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont>'
            f'<a:minorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme>'
            f'<a:fmtScheme name="Office"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill>'
            f'<a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst>'
            f'<a:lnStyleLst><a:ln><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>'
            f'<a:ln><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>'
            f'<a:ln><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst>'
            f'<a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle>'
            f'<a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>'
            f'<a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill>'
            f'<a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst>'
            f'</a:fmtScheme></a:themeElements></a:theme>',
    }
    return zip_parts(parts, pad_to)


def html(pad_to=0):
    body = "".join(f"<p>Fyredocs load-test line {i}: lorem ipsum dolor sit amet.</p>" for i in range(50))
    pad = ("<!-- " + "x" * max(0, pad_to - 2000) + " -->") if pad_to else ""
    return (f"<!doctype html><html><head><meta charset='utf-8'><title>LT</title></head>"
            f"<body><h1>Fyredocs</h1>{body}{pad}</body></html>").encode("utf-8")


def main():
    only = sys.argv[1] if len(sys.argv) > 1 else "all"
    print(f"Generating fixtures -> {OUT}")
    # image PNG dimensions per size
    png_dims = {"small": (600, 800), "medium": (1500, 2000), "large": (3000, 4000)}
    # scanned-pdf grayscale page dims (~A4 @150dpi) and page counts
    scan_pages = {"small": 1, "medium": 4, "large": 12}
    pdf_pages = {"small": 5, "medium": 5, "large": 5}

    for size, target in TARGETS.items():
        if only in ("all", "pdf"):
            write("pdf", size, "pdf", pdf_text(pdf_pages[size], pad_to=target))
        if only in ("all", "scanned-pdf"):
            write("scanned-pdf", size, "pdf", pdf_image(scan_pages[size], 1240, 1754))
        if only in ("all", "docx"):
            write("docx", size, "docx", docx(pad_to=target))
        if only in ("all", "xlsx"):
            write("xlsx", size, "xlsx", xlsx(pad_to=target))
        if only in ("all", "pptx"):
            write("pptx", size, "pptx", pptx(pad_to=target))
        if only in ("all", "image"):
            w, h = png_dims[size]
            write("image", size, "png", png_bytes(w, h, gray=False, noisy=True))
        if only in ("all", "html"):
            write("html", size, "html", html(pad_to=min(target, 2_000_000)))
    print("done.")


if __name__ == "__main__":
    main()
