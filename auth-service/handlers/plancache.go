package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/redis/go-redis/v9"
	"sync"
	"time"

	"gorm.io/gorm"

	"auth-service/internal/models"
	"fyredocs/shared/config"
	"fyredocs/shared/logger"
)

// Subscription plans are a tiny, rarely-changing set (guest/free/pro/…) but
// they are read by name on every login, refresh, /me, /profile and internal API
// call. The database is remote, so each of those lookups costs a full network
// round-trip. An in-process TTL cache keyed by plan name removes that round-trip
// entirely for the TTL window — strictly better than swapping it for a Redis
// call, since it does no network I/O at all.
//
// Trade-off: a plan-definition change (e.g. raising a plan's max file size)
// takes up to PLAN_CACHE_TTL to propagate. That is acceptable for plan limits;
// tune via the PLAN_CACHE_TTL env var (set to 0 to disable caching).

// cachedPlan is one in-process plan entry with its absolute expiry. The expiry is
// stored per entry rather than tracked globally so a lookup can judge freshness
// without a sweep or eviction goroutine.
type cachedPlan struct {
	plan    models.SubscriptionPlan
	expires time.Time
}

var (
	// planCacheMu guards planCache. RWMutex rather than Mutex because reads vastly
	// outnumber writes — every request that checks a plan limit reads, while writes
	// only happen on a cache miss.
	planCacheMu sync.RWMutex
	// planCache is keyed by plan NAME, not user ID, so it holds a handful of
	// entries regardless of user count and needs no size bound. Do not re-key this
	// by user without adding eviction.
	planCache = map[string]cachedPlan{}
	// planCacheTTL bounds how long a plan-definition change takes to propagate to
	// this process. Set PLAN_CACHE_TTL to 0 to disable caching entirely.
	planCacheTTL = config.GetEnvDuration("PLAN_CACHE_TTL", 5*time.Minute)
)

// lookupPlan returns the subscription plan for the given name, served from the
// in-process cache when fresh and falling back to a single DB query on a miss.
// The bool is false when the plan does not exist (or the DB lookup failed), so
// callers can apply their own default — matching the previous inline behaviour.
func lookupPlan(ctx context.Context, name string) (models.SubscriptionPlan, bool) {
	if name == "" {
		return models.SubscriptionPlan{}, false
	}

	if planCacheTTL > 0 {
		planCacheMu.RLock()
		entry, ok := planCache[name]
		planCacheMu.RUnlock()
		if ok && time.Now().Before(entry.expires) {
			return entry.plan, true
		}
	}

	var plan models.SubscriptionPlan
	if err := models.DB.Where("name = ?", name).First(&plan).Error; err != nil {
		// "Not found" is benign (caller applies a default). A real DB error on this
		// hot path (login/refresh/me) must NOT be silently downgraded to "free".
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.LogWarn(ctx, "db.subscription_plans.lookup", err, "planName", name)
		}
		return models.SubscriptionPlan{}, false
	}

	if planCacheTTL > 0 {
		planCacheMu.Lock()
		planCache[name] = cachedPlan{plan: plan, expires: time.Now().Add(planCacheTTL)}
		planCacheMu.Unlock()
	}
	return plan, true
}

// resolvePlan returns the limits that apply to a user on plan name: the user's
// plan row, else the "free" row when the name is unknown, else — only when the
// database cannot be read at all — lastResortPlan. Every login, refresh and
// /auth/me response and the gateway's plan cache go through here, so a DB edit
// to a plan row is what users get. The previous inline fallbacks hardcoded a
// 25 MB free plan that disagreed with the seeded 50 MB row.
func resolvePlan(ctx context.Context, name string) models.SubscriptionPlan {
	if plan, ok := lookupPlan(ctx, name); ok {
		return plan
	}
	if plan, ok := lookupPlan(ctx, "free"); ok {
		if name != "" && name != "free" {
			logger.LogWarn(ctx, "plan.unknown_name_fallback_free", nil, "planName", name)
		}
		return plan
	}
	logger.LogErr(ctx, "plan.db_unavailable_last_resort", errors.New("no plan row readable"), "planName", name)
	return lastResortPlan
}

// lastResortPlan is used only when not even the "free" row can be read (database
// down). It uses the free plan fallbacks the seed also uses
// (shared/config/plan_limits.go), so an outage never grants more than free.
var lastResortPlan = models.SubscriptionPlan{
	Name: "free", MaxFileSizeMB: config.PlanFreeMaxFileMB, MaxFilesPerJob: config.PlanFreeMaxFiles,
	MaxJobInputMB: config.PlanFreeMaxJobMB, MaxOCRPages: config.PlanFreeMaxOCRPages, RetentionDays: config.PlanFreeRetentionDays,
}

// planCacheEntryFor builds the gateway's view of a plan row.
func planCacheEntryFor(plan models.SubscriptionPlan) planCacheEntry {
	return planCacheEntry{
		Plan:        plan.Name,
		MaxFileMB:   plan.MaxFileSizeMB,
		MaxFiles:    plan.MaxFilesPerJob,
		MaxJobMB:    plan.MaxJobInputMB,
		MaxOCRPages: plan.MaxOCRPages,
	}
}

// GuestPlanCacheKey is where the guest plan row is published for the gateway,
// which has no user ID to look up for anonymous requests. NOTE: cross-service
// contract with api-gateway/internal/plancache/plancache.go (guestKey).
const GuestPlanCacheKey = "plan:guest"

// PublishGuestPlan writes the guest plan row to Redis for the gateway. It has
// no TTL on purpose: if auth-service is down, the last published guest limits
// are a better answer than the gateway's built-in default. Returns an error
// (and publishes nothing) when the guest row cannot be read.
func PublishGuestPlan(ctx context.Context, rdb *redis.Client) error {
	if rdb == nil {
		return nil
	}
	plan, ok := lookupPlan(ctx, "guest")
	if !ok {
		return errors.New("guest plan row not found")
	}
	data, err := json.Marshal(planCacheEntryFor(plan))
	if err != nil {
		return err
	}
	return rdb.Set(ctx, GuestPlanCacheKey, data, 0).Err()
}

// RunGuestPlanPublisher publishes the guest plan now and then every interval
// until ctx ends, so an edit to the guest row reaches the gateway within one
// interval (plus the in-process plan cache). Blocking; run it in a goroutine.
func RunGuestPlanPublisher(ctx context.Context, rdb *redis.Client, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	publish := func() {
		if err := PublishGuestPlan(ctx, rdb); err != nil {
			logger.LogWarn(ctx, "plan.publish_guest", err)
		}
	}
	publish()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}
