# Free PDF Processing Tools Setup

## ✅ Migrated to Free Open-Source Tools

**ConvertAPI has been replaced** with free, open-source alternatives. No API keys or paid subscriptions needed!

---

## 🆓 Tools Used

### 1. **pdfcpu** (Pure Go PDF processor)
- **Purpose**: PDF operations (merge, split, compress, encrypt, watermark)
- **License**: Apache 2.0 (free, open-source)
- **Installation**: Included as Go dependency
- **Website**: https://pdfcpu.io/

### 2. **LibreOffice** (Office document processor)
- **Purpose**: Convert Office documents (Word, Excel, PowerPoint) to/from PDF
- **License**: MPL v2.0 (free, open-source)
- **Installation**: Installed in Docker container
- **Website**: https://www.libreoffice.org/

### 3. **Poppler Utils** (PDF utilities)
- **Purpose**: PDF to images conversion
- **License**: GPL (free, open-source)
- **Installation**: Installed in Docker container (`pdftoppm` command)
- **Website**: https://poppler.freedesktop.org/

---

## 🎯 Supported Operations

### PDF Operations (using pdfcpu)
| Operation | Status | Notes |
|-----------|--------|-------|
| **Merge PDFs** | ✅ Working | Combine multiple PDFs into one |
| **Split PDF** | ✅ Working | Extract pages into separate PDFs (zipped) |
| **Compress PDF** | ✅ Working | Optimize PDF file size |
| **Protect PDF** | ✅ Working | Add password encryption |
| **Unlock PDF** | ✅ Working | Remove password (if you have it) |
| **Watermark PDF** | ✅ Working | Add text watermark |

### Office Conversions (using LibreOffice)
| Operation | Status | Notes |
|-----------|--------|-------|
| **Word to PDF** | ✅ Working | .docx → .pdf |
| **Excel to PDF** | ✅ Working | .xlsx → .pdf |
| **PowerPoint to PDF** | ✅ Working | .pptx → .pdf |
| **PDF to Word** | ✅ Working | Simple PDFs work well. Complex layouts may need manual adjustment |
| **PDF to Excel** | ⚠️ Limited | Best for PDFs containing tables |
| **PDF to PowerPoint** | ✅ Working | Each PDF page becomes a slide |

### Image Operations
| Operation | Status | Notes |
|-----------|--------|-------|
| **PDF to Images** | ✅ Working | Extract pages as PNG images (zipped) |
| **Images to PDF** | ✅ Working | Combine multiple images into one PDF |

---

## 📦 Docker Setup

The tools are automatically installed in the Docker container. No manual setup required!

**Dockerfile includes**:
```dockerfile
RUN apk add --no-cache ca-certificates \
    poppler-utils \
    libreoffice \
    ttf-liberation
```

---

## 🚀 Quick Start

1. **Deploy with the updated code**:
   ```bash
   ./deploy.sh
   ```

2. **Test a conversion**:
   ```bash
   # Example: Merge two PDFs
   curl -X POST http://localhost:8081/api/convert-from-pdf/merge-pdf \
     -H "Authorization: Bearer YOUR_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{
       "uploadIds": ["upload-id-1", "upload-id-2"],
       "tool": "merge-pdf"
     }'
   ```

3. **Check the result**:
   ```bash
   # Download the merged PDF
   curl http://localhost:8081/api/convert-from-pdf/merge-pdf/JOB_ID/download \
     -H "Authorization: Bearer YOUR_TOKEN" \
     --output merged.pdf
   ```

---

## 🎨 Usage Examples

### Merge Multiple PDFs
```bash
curl -X POST http://localhost:8081/api/convert-from-pdf/merge-pdf \
  -H "Authorization: Bearer TOKEN" \
  -d '{"uploadIds": ["id1", "id2", "id3"]}'
```

### Split PDF into Pages
```bash
curl -X POST http://localhost:8081/api/convert-from-pdf/split-pdf \
  -H "Authorization: Bearer TOKEN" \
  -d '{"uploadId": "id", "options": {"range": "1-5"}}'
```

### Compress PDF
```bash
curl -X POST http://localhost:8081/api/convert-from-pdf/compress-pdf \
  -H "Authorization: Bearer TOKEN" \
  -d '{"uploadId": "id"}'
```

### Protect PDF with Password
```bash
curl -X POST http://localhost:8081/api/convert-from-pdf/protect-pdf \
  -H "Authorization: Bearer TOKEN" \
  -d '{"uploadId": "id", "options": {"password": "secret123"}}'
```

### Word to PDF
```bash
curl -X POST http://localhost:8081/api/convert-to-pdf/word-to-pdf \
  -H "Authorization: Bearer TOKEN" \
  -d '{"uploadId": "id"}'
```

### PDF to Images
```bash
curl -X POST http://localhost:8081/api/convert-from-pdf/pdf-to-image \
  -H "Authorization: Bearer TOKEN" \
  -d '{"uploadId": "id"}'
# Returns a ZIP file with PNG images of each page
```

### Add Watermark
```bash
curl -X POST http://localhost:8081/api/convert-from-pdf/watermark-pdf \
  -H "Authorization: Bearer TOKEN" \
  -d '{
    "uploadId": "id",
    "options": {"text": "CONFIDENTIAL"}
  }'
```

---

## 🔧 Advanced Configuration

### Custom Watermark Styling
```go
// In processing_free.go, modify watermarkPDF function
wm, err := pdfcpu.ParseTextWatermarkDetails(
    watermarkText,
    "font:Helvetica, points:48, color:1.0 0.0 0.0, opacity:0.5, rotation:45",
    false
)
```

Options:
- **font**: Helvetica, Times, Courier
- **points**: Font size (default: 48)
- **color**: RGB values (0.0-1.0, e.g., "1.0 0.0 0.0" = red)
- **opacity**: 0.0 (transparent) to 1.0 (opaque)
- **rotation**: Degrees (e.g., 45 for diagonal)

### PDF Compression Level
```go
// In processing_free.go, modify compressPDF function
conf := model.NewDefaultConfiguration()
conf.OptimizeDuplicateContentStreams = true
conf.ImageQuality = 75 // 1-100, lower = smaller file
```

---

## 📊 Performance Comparison

### ConvertAPI (Paid) vs Free Tools

| Aspect | ConvertAPI | Free Tools |
|--------|------------|------------|
| **Cost** | $9.99+/month | $0 (free forever) |
| **PDF Operations** | Very fast | Fast (native Go) |
| **Office Conversions** | Very good quality | Good quality |
| **PDF to Office** | Excellent | Limited |
| **Deployment** | API calls (external) | Self-hosted (private) |
| **Privacy** | Files sent to 3rd party | Files stay on your server |
| **Rate Limits** | 250-1500/month | Unlimited |

---

## ⚠️ Limitations & Workarounds

### 1. PDF to Office Conversions
**Issue**: Complex PDF layouts don't convert perfectly to Word/Excel/PowerPoint

**Workaround**:
- For editing PDFs, use LibreOffice Draw directly
- For text extraction, use pdfcpu text extraction
- For data extraction, consider specialized tools like Tabula (for tables)

### 2. Large File Processing
**Issue**: Very large PDFs (100+ MB) may be slow

**Workaround**:
- Compress PDFs first using `compress-pdf` endpoint
- Split large PDFs into smaller chunks
- Increase Docker memory limits if needed

### 3. Font Support
**Issue**: Some rare fonts might not be available

**Workaround**:
- The Dockerfile includes `ttf-liberation` for common fonts
- Add more fonts by updating the Dockerfile:
  ```dockerfile
  RUN apk add --no-cache font-adobe-source-code-pro
  ```

---

## 🐛 Troubleshooting

### Error: "LibreOffice not available"
**Cause**: LibreOffice not installed in container

**Solution**:
```bash
# Rebuild containers
./deploy.sh
```

### Error: "pdftoppm: command not found"
**Cause**: Poppler-utils not installed

**Solution**: Check Dockerfile has `poppler-utils` in the apk install line

### Conversion takes too long
**Solution**: Increase Docker container resources in docker-compose.yml:
```yaml
convert-from-pdf:
  deploy:
    resources:
      limits:
        cpus: '2.0'
        memory: 2G
```

---

## 📚 Technical Details

### pdfcpu Library
- **Pure Go implementation** (no C dependencies)
- **PDF 1.7 compliant**
- **Fast and memory-efficient**
- GitHub: https://github.com/pdfcpu/pdfcpu

### LibreOffice Headless Mode
- Runs without GUI
- Supports all Office formats
- Command: `libreoffice --headless --convert-to pdf`

### Poppler pdftoppm
- Part of Poppler PDF rendering library
- Converts PDF pages to images
- Supports PNG, JPEG, TIFF formats

---

## 🎉 Benefits of Free Tools

1. **No Costs**: Zero subscription fees
2. **Privacy**: Files never leave your server
3. **Unlimited**: No monthly conversion limits
4. **Open Source**: Full control over the code
5. **Self-Hosted**: No external API dependencies
6. **Offline**: Works without internet connection

---

## 🚀 Future Enhancements

Possible additions:
- **OCR Support**: Add Tesseract for text recognition in scanned PDFs
- **PDF Form Filling**: Use pdfcpu form filling capabilities
- **Advanced Watermarks**: Image watermarks, page-specific watermarks
- **Batch Processing**: Process multiple files in parallel
- **Format Validation**: Verify PDF/A compliance

---

## 📝 Migration Notes

If you were previously using ConvertAPI:

1. **No secrets needed**: Remove `CONVERT_API_SECRET` from .env files
2. **Same API endpoints**: All existing endpoints still work
3. **Same request/response format**: No changes to your client code
4. **Better privacy**: Files are processed locally

**What Changed**:
- Backend implementation switched from API calls to local processing
- Added LibreOffice and Poppler to Docker containers
- Added pdfcpu Go library dependency

**What Stayed the Same**:
- API endpoint URLs
- Request/response JSON formats
- Authentication and authorization
- File upload/download flow

---

**Your PDF processing is now 100% free and self-hosted!** 🎉
