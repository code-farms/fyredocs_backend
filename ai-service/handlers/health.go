// Package handlers implements ai-service's HTTP endpoints: health probes and
// the AI op endpoint that validates input, assembles the server-owned prompt,
// and streams the model's answer back as SSE or a JSON envelope.
package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"ai-service/internal/models"
)

// ServiceStartTime is set in main.go at startup.
var ServiceStartTime time.Time

// HealthCheck is a liveness probe that always reports the process is running.
func HealthCheck(c *gin.Context) {
	c.String(http.StatusOK, "ok")
}

// ReadyCheck is a readiness probe that reports 503 unless the database is
// reachable. The model provider is deliberately NOT a readiness dependency:
// an unconfigured or down provider answers AI_UNAVAILABLE per request, and
// pulling the pod out of rotation would turn that specific error into a
// gateway 502 the app can only describe generically.
func ReadyCheck(c *gin.Context) {
	checks := gin.H{}
	if err := models.DB.Exec("SELECT 1").Error; err != nil {
		// Raw driver errors can leak internal host:port details; the probe
		// body stays generic and the cause goes to logs.
		slog.Warn("readyz dependency check failed", "dependency", "postgres", "error", err)
		checks["postgres"] = "unavailable"
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "checks": checks})
		return
	}
	checks["postgres"] = "ok"
	c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
}
