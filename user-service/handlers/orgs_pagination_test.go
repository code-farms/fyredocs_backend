package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestPageParams guards the H9 pagination cap: list endpoints must never return
// an unbounded result set. Verifies defaults, the 100-row hard cap, and offset.
func TestPageParams(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name                         string
		query                        string
		wantPage, wantLimit, wantOff int
	}{
		{"defaults", "", 1, 25, 0},
		{"explicit", "?page=3&limit=10", 3, 10, 20},
		{"limit capped at 100", "?limit=5000", 1, 100, 0},
		{"invalid falls back", "?page=abc&limit=-4", 1, 25, 0},
		{"zero page falls back", "?page=0", 1, 25, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/"+tc.query, nil)
			page, limit, offset := pageParams(c)
			if page != tc.wantPage || limit != tc.wantLimit || offset != tc.wantOff {
				t.Errorf("pageParams(%q) = (%d,%d,%d), want (%d,%d,%d)",
					tc.query, page, limit, offset, tc.wantPage, tc.wantLimit, tc.wantOff)
			}
		})
	}
}
