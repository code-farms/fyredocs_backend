package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"auth-service/internal/models"
)

// cachePlans seeds the in-process plan cache so resolvePlan and
// PublishGuestPlan can be exercised without a database, and empties it on
// cleanup.
func cachePlans(t *testing.T, plans ...models.SubscriptionPlan) {
	t.Helper()
	planCacheMu.Lock()
	planCache = map[string]cachedPlan{}
	for _, p := range plans {
		planCache[p.Name] = cachedPlan{plan: p, expires: time.Now().Add(time.Minute)}
	}
	planCacheMu.Unlock()
	t.Cleanup(func() {
		planCacheMu.Lock()
		planCache = map[string]cachedPlan{}
		planCacheMu.Unlock()
	})
}

func newMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

var (
	testGuestPlan = models.SubscriptionPlan{Name: "guest", MaxFileSizeMB: 20, MaxFilesPerJob: 5, MaxJobInputMB: 100, MaxOCRPages: 10}
	testFreePlan  = models.SubscriptionPlan{Name: "free", MaxFileSizeMB: 50, MaxFilesPerJob: 10, MaxJobInputMB: 300, MaxOCRPages: 30, RetentionDays: 7}
	testProPlan   = models.SubscriptionPlan{Name: "pro", MaxFileSizeMB: 300, MaxFilesPerJob: 50, MaxJobInputMB: 300, MaxOCRPages: 200, RetentionDays: 30}
)

func TestBuildUserResponseCarriesPlanLimits(t *testing.T) {
	user := models.User{ID: uuid.New(), Email: "p@example.com", PlanName: "pro"}
	raw, err := json.Marshal(buildUserResponse(user, "user", testProPlan))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		PlanName   string         `json:"planName"`
		PlanLimits map[string]any `json:"planLimits"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name": "pro", "maxFileSizeMb": float64(300), "maxFilesPerJob": float64(50),
		"maxJobInputMb": float64(300), "maxOcrPages": float64(200), "retentionDays": float64(30),
	}
	if got.PlanName != "pro" {
		t.Errorf("planName = %q, want pro", got.PlanName)
	}
	for k, v := range want {
		if got.PlanLimits[k] != v {
			t.Errorf("planLimits.%s = %v, want %v (full: %s)", k, got.PlanLimits[k], v, raw)
		}
	}
}

func TestLimitsFromPlanEmptyIsNil(t *testing.T) {
	if l := limitsFromPlan(models.SubscriptionPlan{}); l != nil {
		t.Errorf("limitsFromPlan(empty) = %+v, want nil so planLimits is omitted", l)
	}
}

func TestPlanCacheEntryForWireFormat(t *testing.T) {
	raw, _ := json.Marshal(planCacheEntryFor(testProPlan))
	// NOTE: these keys are read by api-gateway/internal/plancache/plancache.go.
	const want = `{"plan":"pro","max_file_mb":300,"max_files":50,"max_job_mb":300,"max_ocr_pages":200}`
	if string(raw) != want {
		t.Errorf("entry = %s, want %s", raw, want)
	}
}

func TestResolvePlan(t *testing.T) {
	cachePlans(t, testFreePlan, testProPlan)
	if got := resolvePlan(context.Background(), "pro"); got != testProPlan {
		t.Errorf("resolvePlan(pro) = %+v, want the pro row", got)
	}
	if got := resolvePlan(context.Background(), ""); got != testFreePlan {
		t.Errorf("resolvePlan(\"\") = %+v, want the free row", got)
	}
}

func TestCachePlanInfoWritesUserEntry(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	ae := &AuthEndpoints{RedisClient: rdb}
	ae.cachePlanInfo(context.Background(), "u1", testProPlan, 15*time.Minute)
	got, err := mr.Get(planCacheKeyPrefix + "u1")
	if err != nil {
		t.Fatalf("no plan cache entry: %v", err)
	}
	var e planCacheEntry
	_ = json.Unmarshal([]byte(got), &e)
	if e != planCacheEntryFor(testProPlan) {
		t.Errorf("entry = %+v, want %+v", e, planCacheEntryFor(testProPlan))
	}
	if ttl := mr.TTL(planCacheKeyPrefix + "u1"); ttl != 15*time.Minute {
		t.Errorf("TTL = %v, want the access-token lifetime 15m", ttl)
	}
}

func TestPublishGuestPlan(t *testing.T) {
	t.Run("publishes the guest row without TTL", func(t *testing.T) {
		cachePlans(t, testGuestPlan)
		mr, rdb := newMiniRedis(t)
		if err := PublishGuestPlan(context.Background(), rdb); err != nil {
			t.Fatal(err)
		}
		got, err := mr.Get(GuestPlanCacheKey)
		if err != nil {
			t.Fatalf("plan:guest not written: %v", err)
		}
		const want = `{"plan":"guest","max_file_mb":20,"max_files":5,"max_job_mb":100,"max_ocr_pages":10}`
		if got != want {
			t.Errorf("plan:guest = %s, want %s", got, want)
		}
		if ttl := mr.TTL(GuestPlanCacheKey); ttl != 0 {
			t.Errorf("TTL = %v, want none", ttl)
		}
	})
	t.Run("nil client is a no-op", func(t *testing.T) {
		if err := PublishGuestPlan(context.Background(), nil); err != nil {
			t.Errorf("nil client: %v", err)
		}
	})
}
