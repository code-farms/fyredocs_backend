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

// MaxUploadBytes is the absolute upload size ceiling across all plans
// (MAX_UPLOAD_MB, default 500 MiB). This is a hard safety ceiling — NOT the
// per-plan limit — and must sit at/above the largest plan (pro = 500 MB) or that
// plan's limit becomes unreachable. Non-positive values fall back.
func MaxUploadBytes() int64 {
	mb := GetEnvInt("MAX_UPLOAD_MB", 500)
	if mb <= 0 {
		mb = 500
	}
	return int64(mb) * 1024 * 1024
}

// Guest plan limits. The DB `subscription_plans` table is the runtime source of
// truth (served to the frontend via /auth/plans and used for authenticated
// enforcement); these helpers seed the guest row and are the fallback applied to
// unauthenticated requests, so the guest limit never drifts between the DB and
// the gateway's enforcement. Override via env for a one-place change.

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
