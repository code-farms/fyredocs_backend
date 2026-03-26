package routing

import (
	"testing"
)

func TestServiceForTool(t *testing.T) {
	tests := []struct {
		tool string
		want string
	}{
		// convert-from-pdf
		{"pdf-to-word", "convert-from-pdf"},
		{"pdf-to-excel", "convert-from-pdf"},
		{"pdf-to-powerpoint", "convert-from-pdf"},
		{"pdf-to-image", "convert-from-pdf"},
		{"pdf-to-text", "convert-from-pdf"},
		{"pdf-to-html", "convert-from-pdf"},
		{"pdf-to-pdfa", "convert-from-pdf"},

		// convert-to-pdf (office/image → PDF only)
		{"word-to-pdf", "convert-to-pdf"},
		{"excel-to-pdf", "convert-to-pdf"},
		{"powerpoint-to-pdf", "convert-to-pdf"},
		{"image-to-pdf", "convert-to-pdf"},
		{"html-to-pdf", "convert-to-pdf"},

		// organize-pdf (fast pdfcpu-based manipulation)
		{"merge-pdf", "organize-pdf"},
		{"split-pdf", "organize-pdf"},
		{"rotate-pdf", "organize-pdf"},
		{"remove-pages", "organize-pdf"},
		{"extract-pages", "organize-pdf"},
		{"organize-pdf", "organize-pdf"},
		{"scan-to-pdf", "organize-pdf"},
		{"watermark-pdf", "organize-pdf"},
		{"protect-pdf", "organize-pdf"},
		{"unlock-pdf", "organize-pdf"},
		{"sign-pdf", "organize-pdf"},
		{"edit-pdf", "organize-pdf"},
		{"add-page-numbers", "organize-pdf"},

		// optimize-pdf (heavy processing)
		{"compress-pdf", "optimize-pdf"},
		{"repair-pdf", "optimize-pdf"},
		{"ocr-pdf", "optimize-pdf"},

		// unknown
		{"unknown-tool", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := ServiceForTool(tt.tool)
			if got != tt.want {
				t.Errorf("ServiceForTool(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestToolServiceMapNotEmpty(t *testing.T) {
	if len(ToolServiceMap) == 0 {
		t.Error("ToolServiceMap should not be empty")
	}
}

func TestAllToolsMappedToValidServices(t *testing.T) {
	validServices := map[string]bool{
		"convert-from-pdf": true,
		"convert-to-pdf":   true,
		"organize-pdf":     true,
		"optimize-pdf":     true,
	}
	for tool, service := range ToolServiceMap {
		if !validServices[service] {
			t.Errorf("tool %q maps to invalid service %q", tool, service)
		}
	}
}
