package plancache

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

const keyPrefix = "user:plan:"

// PlanInfo holds the cached plan data for a user.
type PlanInfo struct {
	Plan      string `json:"plan"`
	MaxFileMB int    `json:"max_file_mb"`
	MaxFiles  int    `json:"max_files"`
}

// DefaultPlan is returned when no cached plan is found.
var DefaultPlan = PlanInfo{
	Plan:      "free",
	MaxFileMB: 25,
	MaxFiles:  10,
}

// GetPlanInfo reads the user's plan info from Redis.
// Returns defaults if the key is missing or on error.
func GetPlanInfo(ctx context.Context, rdb *redis.Client, userID string) PlanInfo {
	if rdb == nil || userID == "" {
		return DefaultPlan
	}

	data, err := rdb.Get(ctx, keyPrefix+userID).Bytes()
	if err != nil {
		if err != redis.Nil {
			slog.Warn("failed to read plan cache from Redis", "error", err, "userID", userID)
		}
		return DefaultPlan
	}

	var info PlanInfo
	if err := json.Unmarshal(data, &info); err != nil {
		slog.Warn("failed to parse plan cache JSON", "error", err, "userID", userID)
		return DefaultPlan
	}

	if info.Plan == "" {
		info.Plan = DefaultPlan.Plan
	}
	if info.MaxFileMB <= 0 {
		info.MaxFileMB = DefaultPlan.MaxFileMB
	}
	if info.MaxFiles <= 0 {
		info.MaxFiles = DefaultPlan.MaxFiles
	}

	return info
}
