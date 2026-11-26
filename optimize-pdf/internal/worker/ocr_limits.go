package worker

import (
	"fmt"
	"time"

	"fyredocs/shared/config"
	"fyredocs/shared/jobslots"

	"optimize-pdf/processing"
)

// OCR is the one tool whose run time grows with the page count: each page is a
// tesseract run (~35 s per page at 0.6 CPU on the 6 vCPU / 12 GB VPS,
// 2026-11-26). With the fixed 10-minute deadline a 300-page scan failed after
// ~17 pages with a vague timeout. So before any OCR work starts the worker:
//
//  1. rejects a scan above the plan's page limit (subscription_plans.max_ocr_pages,
//     carried on the dispatch as MaxOCRPages);
//  2. estimates the run time and rejects a scan one job cannot finish within
//     OCR_MAX_JOB_MINUTES, naming a page count that would fit;
//  3. otherwise gives the job a deadline sized to its pages.
//
// Tunables: OCR_SECONDS_PER_PAGE, OCR_JOB_BASE_SECONDS and OCR_MAX_JOB_MINUTES,
// with their defaults and the AckWait-derived ceiling in constants.go.
func ocrSecondsPerPage() int {
	return positiveEnv("OCR_SECONDS_PER_PAGE", OCRDefaultSecondsPerPage)
}
func ocrJobBaseSeconds() int {
	return positiveEnv("OCR_JOB_BASE_SECONDS", OCRDefaultJobBaseSeconds)
}
func ocrMaxJobMinutes() int {
	return min(positiveEnv("OCR_MAX_JOB_MINUTES", OCRDefaultMaxJobMinutes), OCRMaxJobMinutesCeiling)
}

func positiveEnv(key string, def int) int {
	if n := config.GetEnvInt(key, def); n > 0 {
		return n
	}
	return def
}

// pageLimitError is a permanent OCR rejection. userMessage is what the job's
// failure reason shows; Error() is for logs.
type pageLimitError struct {
	pages, limit int
	userMessage  string
}

func (e *pageLimitError) Error() string {
	return fmt.Sprintf("ocr page limit: %d pages, limit %d", e.pages, e.limit)
}

// ocrParallelism is how many pages of one OCR job run at once: the per-job page
// pool (OCR_MAX_WORKERS), bounded by the worker's cpu tokens, which every
// tesseract draws one of.
func ocrParallelism(slots *jobslots.Slots) int {
	p := processing.OCRWorkers()
	if slots != nil {
		p = min(p, slots.Capacity(jobslots.CPU))
	}
	return max(1, p)
}

// planOCRJob checks an OCR job of the given page count and returns its
// deadline, or a *pageLimitError. planLimit 0 means the dispatch carried no
// limit (an older job-service); only the time check applies then.
func planOCRJob(pages, planLimit, parallel int) (time.Duration, error) {
	if planLimit > 0 && pages > planLimit {
		return 0, &pageLimitError{pages: pages, limit: planLimit, userMessage: fmt.Sprintf(
			"This scan has %d pages, and your plan allows up to %d pages per OCR job. "+
				"Split the PDF into smaller parts, or upgrade your plan for longer scans.", pages, planLimit)}
	}

	parallel = max(1, parallel)
	perPage := time.Duration(ocrSecondsPerPage()) * time.Second
	base := time.Duration(ocrJobBaseSeconds()) * time.Second
	maxJob := time.Duration(ocrMaxJobMinutes()) * time.Minute

	rounds := (pages + parallel - 1) / parallel
	need := base + time.Duration(rounds)*perPage
	if need > maxJob {
		fit := int((maxJob-base)/perPage) * parallel
		return 0, &pageLimitError{pages: pages, limit: fit, userMessage: fmt.Sprintf(
			"This scan has %d pages, which is more than we can OCR in one job. "+
				"Please split it into parts of up to %d pages.", pages, max(1, fit))}
	}

	// Headroom for a slow page or a busy worker; never below the old fixed
	// deadline, never past the cap.
	return min(max(need*OCRDeadlineHeadroomPercent/100, DefaultJobDeadline), maxJob), nil
}
