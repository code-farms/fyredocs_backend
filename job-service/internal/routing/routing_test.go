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
		{"ocr", "convert-from-pdf"},
		{"pdf-to-text", "convert-from-pdf"},
		{"pdf-to-html", "convert-from-pdf"},

		// convert-to-pdf
		{"word-to-pdf", "convert-to-pdf"},
		{"excel-to-pdf", "convert-to-pdf"},
		{"powerpoint-to-pdf", "convert-to-pdf"},
		{"image-to-pdf", "convert-to-pdf"},
		{"merge-pdf", "convert-to-pdf"},
		{"split-pdf", "organize-pdf"},
		{"compress-pdf", "optimize-pdf"},
		{"protect-pdf", "convert-to-pdf"},
		{"unlock-pdf", "convert-to-pdf"},
		{"watermark-pdf", "convert-to-pdf"},

		// organize-pdf
		{"remove-pages", "organize-pdf"},
		{"extract-pages", "organize-pdf"},

		// optimize-pdf
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
