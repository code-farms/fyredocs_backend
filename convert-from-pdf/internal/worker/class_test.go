package worker

import (
	"testing"

	"fyredocs/shared/jobslots"
)

func TestClassForEveryAllowedTool(t *testing.T) {
	for tool, want := range classTable {
		if got := classFor(tool); got != want {
			t.Errorf("classFor(%q) = %q, want %q", tool, got, want)
		}
	}
	if got := classFor("not-a-tool"); got != unknownToolClass {
		t.Errorf("unknown tool class = %q, want %q", got, unknownToolClass)
	}
}

// classTable is the expected class of each tool (the point of the split:
// cheap pdfcpu tools are io, engines are cpu).
var classTable = map[string]jobslots.Class{
	"pdf-to-image": jobslots.CPU, "pdf-to-pdfa": jobslots.CPU, "pdf-to-word": jobslots.CPU,
	"pdf-to-excel": jobslots.CPU, "pdf-to-text": jobslots.CPU, "pdf-to-odt": jobslots.CPU,
}

const unknownToolClass = jobslots.CPU
