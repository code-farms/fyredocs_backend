package config

import "time"

// Canonical env-driven retention and cleanup knobs.
//
// job-service and its cleanup binary both read these values. The fallbacks
// live here, in exactly one place, because divergent copies once meant a
// missing FREE_JOB_TTL would make cleanup expire free users' jobs after 24
// hours while job-service promised 7 days.

// GuestJobTTL is how long guest jobs and their files are retained
// (GUEST_JOB_TTL, default 30m).
func GuestJobTTL() time.Duration {
	return GetEnvDuration("GUEST_JOB_TTL", 30*time.Minute)
}

// FreeJobTTL is how long free-plan jobs and their files are retained
// (FREE_JOB_TTL, default 7 days).
func FreeJobTTL() time.Duration {
	return GetEnvDuration("FREE_JOB_TTL", 7*24*time.Hour)
}

// ProJobTTL is how long pro-plan jobs and their files are retained
// (PRO_JOB_TTL, default 30 days).
func ProJobTTL() time.Duration {
	return GetEnvDuration("PRO_JOB_TTL", 30*24*time.Hour)
}

// UploadTTL is the lifetime of a presigned-multipart upload session — the
// Redis state's expiry (UPLOAD_TTL, default 30m).
func UploadTTL() time.Duration {
	return GetEnvDuration("UPLOAD_TTL", 30*time.Minute)
}

// CleanupInterval is how often the cleanup binary runs a sweep
// (CLEANUP_INTERVAL, default 15m).
func CleanupInterval() time.Duration {
	return GetEnvDuration("CLEANUP_INTERVAL", 15*time.Minute)
}

// StaleMultipartAge is how old an incomplete S3 multipart upload must be
// before cleanup aborts it (MULTIPART_ABORT_TTL, default 24h). There is no
// bucket lifecycle rule; this fully owns aborting multiparts whose Redis
// session vanished without an abort.
func StaleMultipartAge() time.Duration {
	return GetEnvDuration("MULTIPART_ABORT_TTL", 24*time.Hour)
}

// MaxUploadBytes is the worker-capacity ceiling on upload size (MAX_UPLOAD_MB,
// default 300 MiB). It is NOT a plan limit: plan limits live in auth-service's
// subscription_plans table and reach job-service as X-User-Plan-* headers.
// job-service enforces min(plan value, this ceiling) per file and per job, so a
// plan row set above what workers can hold still cannot admit a job the workers
// will reject. 300 matches the PDF workers' tmpfs guard: total input × 3 must fit
// their 900 MB budget.
//
// NOTE: keep in sync with the 900 MB budget in */internal/worker/tmpfs.go and the
// max_job_input_mb values in auth-service/internal/migrations/00002_plan_job_input_limit.sql.
// Non-positive or unparsable values fall back to the default.
func MaxUploadBytes() int64 {
	mb := GetEnvInt("MAX_UPLOAD_MB", 300)
	if mb <= 0 {
		mb = 300
	}
	return int64(mb) * 1024 * 1024
}

// Guest plan fallbacks. The guest row in auth-service's subscription_plans table
// is the source of guest limits: auth-service publishes it to Redis (plan:guest)
// and the gateway forwards it as X-User-Plan-* headers. These helpers are used
// only when that header is missing (Redis down, gateway bypassed), so a request
// is never left without a limit. Their defaults match the seeded guest row.

// GuestMaxFileSizeMB is the guest plan's per-file upload limit in MB
// (GUEST_MAX_FILE_MB, default 20).
func GuestMaxFileSizeMB() int {
	mb := GetEnvInt("GUEST_MAX_FILE_MB", 20)
	if mb <= 0 {
		mb = 20
	}
	return mb
}

// GuestMaxFilesPerJob is the guest plan's per-job file count limit
// (GUEST_MAX_FILES, default 5).
func GuestMaxFilesPerJob() int {
	n := GetEnvInt("GUEST_MAX_FILES", 5)
	if n <= 0 {
		n = 5
	}
	return n
}

// GuestMaxJobInputMB is the fallback cap on the total size of all files in one
// guest job, in MB (GUEST_MAX_JOB_MB, default 100 = 20 MB × 5 files).
func GuestMaxJobInputMB() int {
	mb := GetEnvInt("GUEST_MAX_JOB_MB", 100)
	if mb <= 0 {
		mb = 100
	}
	return mb
}
