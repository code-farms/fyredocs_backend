package worker

import "time"

// Worker timing and OCR sizing defaults: the one place these numbers live in
// optimize-pdf. The OCR values are host-speed tunables, not plan data (plan
// limits are the subscription_plans rows); each can be overridden by the env
// var named next to it without a rebuild.
const (
	// ConsumerAckWait is how long JetStream waits for an ack before
	// redelivering a message. Every job deadline must stay below it, or a job
	// still running is handed to a second worker.
	ConsumerAckWait = 30 * time.Minute

	// DefaultJobDeadline bounds every job that is not OCR, so a hung
	// subprocess cannot run into ConsumerAckWait.
	DefaultJobDeadline = 10 * time.Minute

	// OCRDefaultSecondsPerPage is the per-page budget for one tesseract slot
	// (OCR_SECONDS_PER_PAGE). Measured ~35 s at 0.6 CPU on the 6 vCPU / 12 GB
	// VPS (2026-11-26); faster hosts can lower it.
	OCRDefaultSecondsPerPage = 40
	// OCRDefaultJobBaseSeconds is the fixed cost of an OCR job: download,
	// page count, merge (OCR_JOB_BASE_SECONDS).
	OCRDefaultJobBaseSeconds = 60
	// OCRDefaultMaxJobMinutes is the longest an OCR job may run
	// (OCR_MAX_JOB_MINUTES).
	OCRDefaultMaxJobMinutes = 25
	// OCRMaxJobMinutesCeiling clamps OCR_MAX_JOB_MINUTES: ConsumerAckWait
	// (30 min) minus headroom for the drain and the final upload.
	OCRMaxJobMinutesCeiling = 28
	// OCRDeadlineHeadroomPercent is the OCR deadline as a share of the
	// estimated run time: room for a slow page or a busy worker.
	OCRDeadlineHeadroomPercent = 150
)
