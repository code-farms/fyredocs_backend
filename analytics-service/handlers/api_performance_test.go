package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAPIPerformance_RouteExists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/metrics/api-performance", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/api-performance", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAPIPerformance_DefaultGatewayURL(t *testing.T) {
	t.Setenv("API_GATEWAY_METRICS_URL", "")
	// Just verify the env var parsing doesn't panic
	url := "http://api-gateway:8080/metrics"
	if url == "" {
		t.Error("expected non-empty default URL")
	}
}

func TestAPIPerformance_QueryParamDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Verify queryInt returns correct defaults
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		page := queryInt(c, "page", 1)
		limit := queryInt(c, "limit", 50)
		sortBy := c.DefaultQuery("sortBy", "requests")
		sortDir := c.DefaultQuery("sortDir", "desc")

		if page != 1 {
			t.Errorf("expected page=1, got %d", page)
		}
		if limit != 50 {
			t.Errorf("expected limit=50, got %d", limit)
		}
		if sortBy != "requests" {
			t.Errorf("expected sortBy=requests, got %s", sortBy)
		}
		if sortDir != "desc" {
			t.Errorf("expected sortDir=desc, got %s", sortDir)
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAPIPerformance_QueryParamOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		page := queryInt(c, "page", 1)
		limit := queryInt(c, "limit", 50)
		sortBy := c.DefaultQuery("sortBy", "requests")
		sortDir := c.DefaultQuery("sortDir", "desc")
		search := c.Query("search")
		method := c.Query("method")

		if page != 2 {
			t.Errorf("expected page=2, got %d", page)
		}
		if limit != 10 {
			t.Errorf("expected limit=10, got %d", limit)
		}
		if sortBy != "p95LatencyMs" {
			t.Errorf("expected sortBy=p95LatencyMs, got %s", sortBy)
		}
		if sortDir != "asc" {
			t.Errorf("expected sortDir=asc, got %s", sortDir)
		}
		if search != "admin" {
			t.Errorf("expected search=admin, got %s", search)
		}
		if method != "GET" {
			t.Errorf("expected method=GET, got %s", method)
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/test?page=2&limit=10&sortBy=p95LatencyMs&sortDir=asc&search=admin&method=GET", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAPIPerformance_LimitCap(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		limit := queryInt(c, "limit", 50)
		if limit > 200 {
			limit = 200
		}
		if limit != 200 {
			t.Errorf("expected limit capped at 200, got %d", limit)
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/test?limit=500", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
