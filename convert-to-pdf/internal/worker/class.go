package worker

import "fyredocs/shared/jobslots"

// classFor maps a tool to its workload class (shared/jobslots). image-to-pdf is
// a pdfcpu import (io); every other tool runs LibreOffice (cpu), so an image
// conversion no longer waits behind office documents.
func classFor(tool string) jobslots.Class {
	switch tool {
	case "image-to-pdf", "img-to-pdf":
		return jobslots.IO
	}
	return jobslots.CPU
}
