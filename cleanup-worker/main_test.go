package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestCleanupInterval(t *testing.T) {
	t.Run("default 15m", func(t *testing.T) {
		t.Setenv("CLEANUP_INTERVAL", "")
		got := cleanupInterval()
		if got != 15*time.Minute {
			t.Errorf("expected 15m, got %v", got)
		}
	})

	t.Run("custom 5m", func(t *testing.T) {
		t.Setenv("CLEANUP_INTERVAL", "5m")
		got := cleanupInterval()
		if got != 5*time.Minute {
			t.Errorf("expected 5m, got %v", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("CLEANUP_INTERVAL", "invalid")
		got := cleanupInterval()
		if got != 15*time.Minute {
			t.Errorf("expected 15m, got %v", got)
		}
	})
}

func TestUploadTTL(t *testing.T) {
	t.Run("default 2h", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "")
		got := uploadTTL()
		if got != 2*time.Hour {
			t.Errorf("expected 2h, got %v", got)
		}
	})

	t.Run("custom 30m", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "30m")
		got := uploadTTL()
		if got != 30*time.Minute {
			t.Errorf("expected 30m, got %v", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("UPLOAD_TTL", "notaduration")
		got := uploadTTL()
		if got != 2*time.Hour {
			t.Errorf("expected 2h, got %v", got)
		}
	})
}

func TestUploadBaseDir(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("UPLOAD_DIR", "")
		got := uploadBaseDir()
		if got != "uploads" {
			t.Errorf("expected 'uploads', got %q", got)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("UPLOAD_DIR", "/data/uploads")
		got := uploadBaseDir()
		if got != "/data/uploads" {
			t.Errorf("expected '/data/uploads', got %q", got)
		}
	})
}

func TestOutputBaseDir(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("OUTPUT_DIR", "")
		got := outputBaseDir()
		if got != "outputs" {
			t.Errorf("expected 'outputs', got %q", got)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("OUTPUT_DIR", "/data/outputs")
		got := outputBaseDir()
		if got != "/data/outputs" {
			t.Errorf("expected '/data/outputs', got %q", got)
		}
	})
}

func TestFreeJobTTL(t *testing.T) {
	t.Run("default 24h", func(t *testing.T) {
		t.Setenv("FREE_JOB_TTL", "")
		got := freeJobTTL()
		if got != 24*time.Hour {
			t.Errorf("expected 24h, got %v", got)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("FREE_JOB_TTL", "12h")
		got := freeJobTTL()
		if got != 12*time.Hour {
			t.Errorf("expected 12h, got %v", got)
		}
	})

	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("FREE_JOB_TTL", "invalid")
		got := freeJobTTL()
		if got != 24*time.Hour {
			t.Errorf("expected 24h, got %v", got)
		}
	})
}

func TestHealthzRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestReadyzRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/readyz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestDefaultPort(t *testing.T) {
	t.Setenv("PORT", "")
	port := os.Getenv("PORT")
	if port == "" {
		port = "8088"
	}
	if port != "8088" {
		t.Errorf("expected default port 8088, got %s", port)
	}
}

func TestOutputFileJobIDRegexp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantID  string
		wantOK  bool
	}{
		{"optimized file", "optimized_a7786d18-0ec1-43aa-ad71-9e1e7c7037ea_1774037055.pdf", "a7786d18-0ec1-43aa-ad71-9e1e7c7037ea", true},
		{"processed file", "processed_3dae81f8-6546-4a4d-8f5b-8479396ba8a7_1774036934.zip", "3dae81f8-6546-4a4d-8f5b-8479396ba8a7", true},
		{"converted file", "converted_3dae81f8-6546-4a4d-8f5b-8479396ba8a7_1774036934.docx", "3dae81f8-6546-4a4d-8f5b-8479396ba8a7", true},
		{"gitkeep", ".gitkeep", "", false},
		{"random file", "random.txt", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := outputFileJobIDRegexp.FindStringSubmatch(tt.input)
			if tt.wantOK {
				if len(matches) < 2 || matches[1] != tt.wantID {
					t.Errorf("expected jobID %q, got matches %v", tt.wantID, matches)
				}
			} else {
				if len(matches) >= 2 {
					t.Errorf("expected no match, got %v", matches)
				}
			}
		})
	}
}
