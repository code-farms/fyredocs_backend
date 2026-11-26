// Package plancache reads the per-user plan limits that auth-service publishes
// to Redis. The gateway consults it on every authenticated request to key rate
// limits and enforce upload ceilings, so lookups fall back to conservative
// defaults rather than failing the request on a cache miss or Redis error.
package plancache

import (
	"context"
	"encoding/json"
	"log/slog"

	"fyredocs/shared/config"

	"github.com/redis/go-redis/v9"
)

// keyPrefix namespaces cached plan entries. auth-service writes these keys on a
// plan change and the gateway reads them, so the prefix is a cross-service
// contract: changing it here without changing the writer makes every lookup miss
// and silently fall back to the default plan limits.
const keyPrefix = "user:plan:"

// guestKey is where auth-service publishes the guest plan row; anonymous
// requests have no user ID to look up. NOTE: cross-service contract with
// auth-service/handlers/plancache.go (GuestPlanCacheKey).
const guestKey = "plan:guest"

// PlanInfo holds the cached plan data for a user. Field names are the JSON
// contract with auth-service's planCacheEntry (auth-service/handlers/auth.go).
type PlanInfo struct {
	Plan      string `json:"plan"`
	MaxFileMB int    `json:"max_file_mb"`
	MaxFiles  int    `json:"max_files"`
	// MaxJobMB is the total size of all files allowed in one job.
	MaxJobMB int `json:"max_job_mb"`
	// MaxOCRPages is the most pages one OCR job may have.
	MaxOCRPages int `json:"max_ocr_pages"`
}

// DefaultPlan is the last resort when no cached plan is found for an
// authenticated user. The real limits live in auth-service's
// subscription_plans table; this uses the free plan fallbacks the seed also
// uses (shared/config/plan_limits.go), so a cache miss never grants more than free.
var DefaultPlan = PlanInfo{
	Plan:        "free",
	MaxFileMB:   config.PlanFreeMaxFileMB,
	MaxFiles:    config.PlanFreeMaxFiles,
	MaxJobMB:    config.PlanFreeMaxJobMB,
	MaxOCRPages: config.PlanFreeMaxOCRPages,
}

// DefaultGuestPlan is the last resort for anonymous requests when auth-service
// has not published the guest row. Uses the guest fallbacks from
// shared/config/plan_limits.go.
var DefaultGuestPlan = PlanInfo{
	Plan:        "guest",
	MaxFileMB:   config.PlanGuestMaxFileMB,
	MaxFiles:    config.PlanGuestMaxFiles,
	MaxJobMB:    config.PlanGuestMaxJobMB,
	MaxOCRPages: config.PlanGuestMaxOCRPages,
}

// GetPlanInfo reads the user's plan info from Redis.
// Returns DefaultPlan if the key is missing or on error.
func GetPlanInfo(ctx context.Context, rdb *redis.Client, userID string) PlanInfo {
	if rdb == nil || userID == "" {
		return DefaultPlan
	}
	return readPlan(ctx, rdb, keyPrefix+userID, DefaultPlan)
}

// GetGuestPlanInfo reads the guest plan row published by auth-service.
// Returns DefaultGuestPlan if it is missing or on error.
func GetGuestPlanInfo(ctx context.Context, rdb *redis.Client) PlanInfo {
	if rdb == nil {
		return DefaultGuestPlan
	}
	return readPlan(ctx, rdb, guestKey, DefaultGuestPlan)
}

// readPlan decodes the plan entry at key, filling any missing or non-positive
// field from fallback. Fail-open by design: a Redis problem must not fail the
// request, only fall back to conservative limits (logged).
func readPlan(ctx context.Context, rdb *redis.Client, key string, fallback PlanInfo) PlanInfo {
	data, err := rdb.Get(ctx, key).Bytes()
	if err != nil {
		if err != redis.Nil {
			slog.Warn("failed to read plan cache from Redis", "error", err, "key", key)
		} else {
			slog.Warn("plan cache miss; using built-in default limits", "key", key, "plan", fallback.Plan)
		}
		return fallback
	}

	var info PlanInfo
	if err := json.Unmarshal(data, &info); err != nil {
		slog.Warn("failed to parse plan cache JSON", "error", err, "key", key)
		return fallback
	}

	if info.Plan == "" {
		info.Plan = fallback.Plan
	}
	if info.MaxFileMB <= 0 {
		info.MaxFileMB = fallback.MaxFileMB
	}
	if info.MaxFiles <= 0 {
		info.MaxFiles = fallback.MaxFiles
	}
	if info.MaxJobMB <= 0 {
		info.MaxJobMB = fallback.MaxJobMB
	}
	if info.MaxOCRPages <= 0 {
		info.MaxOCRPages = fallback.MaxOCRPages
	}
	return info
}
