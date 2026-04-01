package routing

// ToolServiceMap maps tool types to their processing service names.
// This is the single source of truth for queue routing.
//
// Classification principle: group by dependency/performance profile.
//   - convert-to-pdf:  Office → PDF conversions (LibreOffice, heavy)
//   - convert-from-pdf: PDF → other format conversions
//   - organize-pdf:    Fast pdfcpu-based PDF manipulation
//   - optimize-pdf:    Heavy Ghostscript/Tesseract processing
var ToolServiceMap = map[string]string{
	// convert-from-pdf tools (PDF → other formats)
	"pdf-to-word":       "convert-from-pdf",
	"pdf-to-docx":       "convert-from-pdf",
	"pdf-to-excel":      "convert-from-pdf",
	"pdf-to-xlsx":       "convert-from-pdf",
	"pdf-to-powerpoint": "convert-from-pdf",
	"pdf-to-ppt":        "convert-from-pdf",
	"pdf-to-pptx":       "convert-from-pdf",
	"pdf-to-image":      "convert-from-pdf",
	"pdf-to-img":        "convert-from-pdf",
	"pdf-to-html":       "convert-from-pdf",
	"pdf-to-text":       "convert-from-pdf",
	"pdf-to-txt":        "convert-from-pdf",
	"pdf-to-pdfa":       "convert-from-pdf",
	"pdf-to-odt":        "convert-from-pdf",
	"pdf-to-ods":        "convert-from-pdf",
	"pdf-to-odp":        "convert-from-pdf",

	// convert-to-pdf tools (other formats → PDF, LibreOffice-heavy)
	"word-to-pdf":       "convert-to-pdf",
	"excel-to-pdf":      "convert-to-pdf",
	"powerpoint-to-pdf": "convert-to-pdf",
	"ppt-to-pdf":        "convert-to-pdf",
	"html-to-pdf":       "convert-to-pdf",
	"image-to-pdf":      "convert-to-pdf",
	"img-to-pdf":        "convert-to-pdf",
	"odt-to-pdf":        "convert-to-pdf",
	"ods-to-pdf":        "convert-to-pdf",
	"odp-to-pdf":        "convert-to-pdf",
	"word-to-odt":       "convert-to-pdf",
	"excel-to-ods":      "convert-to-pdf",
	"powerpoint-to-odp": "convert-to-pdf",

	// organize-pdf tools (fast pdfcpu-based PDF manipulation)
	"merge-pdf":        "organize-pdf",
	"split-pdf":        "organize-pdf",
	"rotate-pdf":       "organize-pdf",
	"remove-pages":     "organize-pdf",
	"extract-pages":    "organize-pdf",
	"organize-pdf":     "organize-pdf",
	"scan-to-pdf":      "organize-pdf",
	"watermark-pdf":    "organize-pdf",
	"protect-pdf":      "organize-pdf",
	"unlock-pdf":       "organize-pdf",
	"sign-pdf":         "organize-pdf",
	"edit-pdf":         "organize-pdf",
	"add-page-numbers": "organize-pdf",

	// optimize-pdf tools (heavy Ghostscript/Tesseract processing)
	"compress-pdf": "optimize-pdf",
	"repair-pdf":   "optimize-pdf",
	"ocr-pdf":      "optimize-pdf",
}

// ServiceForTool returns the processing service name for the given tool type.
// Returns an empty string if the tool type is unknown.
func ServiceForTool(toolType string) string {
	return ToolServiceMap[toolType]
}
