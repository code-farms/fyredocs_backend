package plancache

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestGetPlanInfoNilClient(t *testing.T) {
	info := GetPlanInfo(context.Background(), nil, "user-123")
	if info.Plan != "free" {
		t.Errorf("expected default plan 'free', got %q", info.Plan)
	}
	if info.MaxFileMB != 50 {
		t.Errorf("expected default MaxFileMB 50, got %d", info.MaxFileMB)
	}
	if info.MaxFiles != 10 {
		t.Errorf("expected default MaxFiles 10, got %d", info.MaxFiles)
	}
}

func TestGetPlanInfoEmptyUserID(t *testing.T) {
	info := GetPlanInfo(context.Background(), nil, "")
	if info != DefaultPlan {
		t.Errorf("expected default plan, got %+v", info)
	}
}

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

// The JSON written below is exactly what auth-service's planCacheEntryFor
// produces (pinned in auth-service/handlers/plan_limits_test.go).
func TestGetPlanInfoReadsUserEntry(t *testing.T) {
	mr, rdb := newRedis(t)
	_ = mr.Set(keyPrefix+"u1", `{"plan":"pro","max_file_mb":300,"max_files":50,"max_job_mb":300,"max_ocr_pages":200}`)
	want := PlanInfo{Plan: "pro", MaxFileMB: 300, MaxFiles: 50, MaxJobMB: 300, MaxOCRPages: 200}
	if got := GetPlanInfo(context.Background(), rdb, "u1"); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetPlanInfoFillsMissingJobField(t *testing.T) {
	// An entry written before max_job_mb existed (still live until its TTL
	// expires after a deploy) gets the free default for the missing field.
	mr, rdb := newRedis(t)
	_ = mr.Set(keyPrefix+"u1", `{"plan":"pro","max_file_mb":300,"max_files":50}`)
	if got := GetPlanInfo(context.Background(), rdb, "u1"); got.MaxJobMB != DefaultPlan.MaxJobMB || got.MaxFileMB != 300 {
		t.Errorf("got %+v, want stored per-file 300 and default job %d", got, DefaultPlan.MaxJobMB)
	}
}

func TestGetPlanInfoFillsMissingOCRPages(t *testing.T) {
	// An entry written before max_ocr_pages existed (live until its TTL expires
	// after a deploy) gets the free default, never 0 = unlimited.
	mr, rdb := newRedis(t)
	_ = mr.Set(keyPrefix+"u1", `{"plan":"pro","max_file_mb":300,"max_files":50,"max_job_mb":300}`)
	if got := GetPlanInfo(context.Background(), rdb, "u1"); got.MaxOCRPages != DefaultPlan.MaxOCRPages {
		t.Errorf("MaxOCRPages = %d, want default %d", got.MaxOCRPages, DefaultPlan.MaxOCRPages)
	}
}

func TestGetPlanInfoMalformedFallsBack(t *testing.T) {
	mr, rdb := newRedis(t)
	_ = mr.Set(keyPrefix+"u1", `not json`)
	if got := GetPlanInfo(context.Background(), rdb, "u1"); got != DefaultPlan {
		t.Errorf("got %+v, want DefaultPlan", got)
	}
}

func TestGetGuestPlanInfo(t *testing.T) {
	t.Run("published row wins", func(t *testing.T) {
		mr, rdb := newRedis(t)
		_ = mr.Set(guestKey, `{"plan":"guest","max_file_mb":15,"max_files":3,"max_job_mb":45,"max_ocr_pages":7}`)
		want := PlanInfo{Plan: "guest", MaxFileMB: 15, MaxFiles: 3, MaxJobMB: 45, MaxOCRPages: 7}
		if got := GetGuestPlanInfo(context.Background(), rdb); got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})
	t.Run("missing row uses DefaultGuestPlan", func(t *testing.T) {
		_, rdb := newRedis(t)
		if got := GetGuestPlanInfo(context.Background(), rdb); got != DefaultGuestPlan {
			t.Errorf("got %+v, want %+v", got, DefaultGuestPlan)
		}
	})
	t.Run("nil client uses DefaultGuestPlan", func(t *testing.T) {
		if got := GetGuestPlanInfo(context.Background(), nil); got != DefaultGuestPlan {
			t.Errorf("got %+v, want %+v", got, DefaultGuestPlan)
		}
	})
}
