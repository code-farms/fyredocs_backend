package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"document-service/internal/models"
)

// ServiceStartTime is set in main.go at startup.
var ServiceStartTime time.Time

// HealthCheck is a liveness probe.
func HealthCheck(c *gin.Context) {
	c.String(http.StatusOK, "ok")
}

// ReadyCheck reports readiness including a DB ping.
func ReadyCheck(c *gin.Context) {
	checks := gin.H{}
	ready := true
	if err := models.DB.Exec("SELECT 1").Error; err != nil {
		checks["postgres"] = err.Error()
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
