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
	"word-to-pdf": jobslots.CPU, "excel-to-pdf": jobslots.CPU, "ppt-to-pdf": jobslots.CPU,
	"html-to-pdf": jobslots.CPU, "odt-to-pdf": jobslots.CPU, "word-to-odt": jobslots.CPU,
	"image-to-pdf": jobslots.IO, "img-to-pdf": jobslots.IO,
}

const unknownToolClass = jobslots.CPU
