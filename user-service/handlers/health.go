package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"user-service/internal/models"
)

// ServiceStartTime is set in main.go at startup.
var ServiceStartTime time.Time

// HealthCheck is a liveness probe that always reports the process is running.
func HealthCheck(c *gin.Context) {
	c.String(http.StatusOK, "ok")
}

// ReadyCheck is a readiness probe that reports 503 unless the database is
// reachable, so traffic is only routed here once dependencies are available.
func ReadyCheck(c *gin.Context) {
	checks := gin.H{}
	ready := true
	if err := models.DB.Exec("SELECT 1").Error; err != nil {
		// Raw driver errors can leak internal host:port details; the probe
		// body stays generic and the cause goes to logs.
		slog.Warn("readyz dependency check failed", "dependency", "postgres", "error", err)
		checks["postgres"] = "unavailable"
		ready = false
	} else {
		checks["postgres"] = "ok"
	}
	if !ready {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "checks": checks})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
}
