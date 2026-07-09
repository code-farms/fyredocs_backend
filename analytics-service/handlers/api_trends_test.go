package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/models"
)

// With no DB (models.DB nil in unit tests) the handler must still return a valid
// envelope with empty — not null — series/errorClasses so the chart can map them.
func TestAPITrends_NoDB_ValidEmptyShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if models.DB != nil {
		t.Skip("DB present; this test covers the no-DB path")
	}

	r := gin.New()
	r.GET("/admin/metrics/api-trends", APITrends)
	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/api-trends?days=7", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Resolution   string        `json:"resolution"`
			Series       []interface{} `json:"series"`
			ErrorClasses []interface{} `json:"errorClasses"`
			Period       struct {
				Days int `json:"days"`
			} `json:"period"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body.String())
	}
	if !body.Success {
		t.Error("success = false, want true")
	}
	if body.Data.Series == nil || body.Data.ErrorClasses == nil {
		t.Errorf("series/errorClasses must be [] not null: %s", w.Body.String())
	}
	if body.Data.Period.Days != 7 {
		t.Errorf("period.days = %d, want 7", body.Data.Period.Days)
	}
	if body.Data.Resolution != "day" {
		t.Errorf("resolution = %q, want day for days=7", body.Data.Resolution)
	}
}

func TestAPITrends_HourResolutionForShortRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if models.DB != nil {
		t.Skip("DB present; this test covers the no-DB path")
	}
	r := gin.New()
	r.GET("/admin/metrics/api-trends", APITrends)
	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/api-trends?days=1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var body struct {
		Data struct {
			Resolution string `json:"resolution"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Data.Resolution != "hour" {
		t.Errorf("resolution = %q, want hour for days=1", body.Data.Resolution)
	}
}
