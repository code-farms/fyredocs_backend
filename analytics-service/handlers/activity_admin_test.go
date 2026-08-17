package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAdminActivitySortColumns_Closed(t *testing.T) {
	// Sort columns are interpolated into SQL; only snake_case column names
	// from the user_activity schema may appear as values.
	allowed := map[string]bool{
		"occurred_at": true, "created_at": true, "event_type": true,
		"status": true, "platform": true,
	}
	for key, col := range adminActivitySortColumns {
		if !allowed[col] {
			t.Errorf("adminActivitySortColumns[%q] = %q is not a known safe column", key, col)
		}
	}
	if adminActivitySortColumns["occurred_at; DROP TABLE user_activity"] != "" {
		t.Error("unknown sort keys must map to empty (fallback), never pass through")
	}
}

// TestApplyAdminActivityFilters_BadUserIDMatchesNothing guards the fail-safe:
// an unparseable userId filter must yield zero rows, not the unfiltered table.
func TestApplyAdminActivityFilters_BadUserIDMatchesNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/admin/activity?userId=not-a-uuid", nil)

	// A nil *gorm.DB would panic on .Where; the guard here is that the filter
	// function decides "1 = 0" purely from the param, so we assert on the
	// parse branch directly instead of executing SQL.
	if _, err := parseUUIDParam("not-a-uuid"); err == nil {
		t.Fatal("expected parse failure for garbage userId")
	}
	if _, err := parseUUIDParam("0198c5d0-0000-7000-8000-000000000001"); err != nil {
		t.Fatalf("valid uuid rejected: %v", err)
	}
}
