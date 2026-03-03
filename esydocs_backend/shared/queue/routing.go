package queue

// ToolServiceMap maps tool types to their processing service names.
// This is the single source of truth for queue routing.
var ToolServiceMap = map[string]string{
	// convert-from-pdf tools
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
	"ocr":               "convert-from-pdf",

	// convert-to-pdf tools
	"word-to-pdf":       "convert-to-pdf",
	"excel-to-pdf":      "convert-to-pdf",
	"powerpoint-to-pdf": "convert-to-pdf",
	"ppt-to-pdf":        "convert-to-pdf",
	"html-to-pdf":       "convert-to-pdf",
	"image-to-pdf":      "convert-to-pdf",
	"img-to-pdf":        "convert-to-pdf",
	"merge-pdf":         "convert-to-pdf",
	"split-pdf":         "convert-to-pdf",
	"compress-pdf":      "convert-to-pdf",
	"page-reorder":      "convert-to-pdf",
	"page-rotate":       "convert-to-pdf",
	"watermark-pdf":     "convert-to-pdf",
	"protect-pdf":       "convert-to-pdf",
	"unlock-pdf":        "convert-to-pdf",
	"sign-pdf":          "convert-to-pdf",
	"edit-pdf":          "convert-to-pdf",

	// organize-pdf tools
	"remove-pages":  "organize-pdf",
	"extract-pages": "organize-pdf",
	"organize-pdf":  "organize-pdf",
	"scan-to-pdf":   "organize-pdf",

	// optimize-pdf tools
	"repair-pdf": "optimize-pdf",
	"ocr-pdf":    "optimize-pdf",
}

// ServiceForTool returns the processing service name for the given tool type.
// Returns an empty string if the tool type is unknown.
func ServiceForTool(toolType string) string {
	return ToolServiceMap[toolType]
}
