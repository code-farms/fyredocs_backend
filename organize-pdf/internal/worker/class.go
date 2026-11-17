package worker

import "fyredocs/shared/jobslots"

// classFor maps a tool to its workload class (shared/jobslots). Every
// organize-pdf tool is pure pdfcpu (io) except scan-to-pdf, which decodes
// photos, runs Go image clean-up and optionally tesseract (cpu). Without the
// split, four scans filled every slot and a 300 ms merge waited behind them
// (12-performance-architecture.md §5.1).
func classFor(tool string) jobslots.Class {
	if tool == "scan-to-pdf" {
		return jobslots.CPU
	}
	return jobslots.IO
}
