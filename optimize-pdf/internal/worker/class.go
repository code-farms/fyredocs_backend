package worker

import "fyredocs/shared/jobslots"

// classFor maps a tool to its workload class (shared/jobslots). Ghostscript
// tools are cpu. OCR takes no cpu token as a job, only a token of the PerUnit
// job pool; its page pool draws one cpu token per page (processing.ocrPDF via
// jobslots.FromContext), so two OCR jobs share CPU_JOB_SLOTS tesseracts instead
// of starting OCR_MAX_WORKERS each.
func classFor(tool string) jobslots.Class {
	if tool == "ocr-pdf" {
		return jobslots.PerUnit
	}
	return jobslots.CPU
}
