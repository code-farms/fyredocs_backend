package handlers

import (
	"sync"
	"time"

	"auth-service/internal/models"
	"fyredocs/shared/config"
)

// Subscription plans are a tiny, rarely-changing set (free/pro/anonymous/…) but
// they are read by name on every login, refresh, /me, /profile and internal API
// call. The database is remote, so each of those lookups costs a full network
// round-trip. An in-process TTL cache keyed by plan name removes that round-trip
// entirely for the TTL window — strictly better than swapping it for a Redis
// call, since it does no network I/O at all.
//
// Trade-off: a plan-definition change (e.g. raising a plan's max file size)
// takes up to PLAN_CACHE_TTL to propagate. That is acceptable for plan limits;
// tune via the PLAN_CACHE_TTL env var (set to 0 to disable caching).

type cachedPlan struct {
	plan    models.SubscriptionPlan
	expires time.Time
}

var (
	planCacheMu  sync.RWMutex
	planCache    = map[string]cachedPlan{}
	planCacheTTL = config.GetEnvDuration("PLAN_CACHE_TTL", 5*time.Minute)
)

// lookupPlan returns the subscription plan for the given name, served from the
// in-process cache when fresh and falling back to a single DB query on a miss.
// The bool is false when the plan does not exist (or the DB lookup failed), so
// callers can apply their own default — matching the previous inline behaviour.
func lookupPlan(name string) (models.SubscriptionPlan, bool) {
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
		return models.SubscriptionPlan{}, false
	}

	if planCacheTTL > 0 {
		planCacheMu.Lock()
		planCache[name] = cachedPlan{plan: plan, expires: time.Now().Add(planCacheTTL)}
		planCacheMu.Unlock()
	}
	return plan, true
}
