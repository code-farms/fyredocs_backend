package handlers

import (
	"auth-service/internal/models"

	"fyredocs/shared/response"

	"github.com/gin-gonic/gin"
)

// GetAllPlans returns all subscription plans and their limits, including guest.
// This is the single source of truth the frontend reads for plan limits.
// GET /auth/plans — no authentication required.
func GetAllPlans(c *gin.Context) {
	var plans []models.SubscriptionPlan
	if err := models.DB.
		Order("max_file_size_mb asc").
		Find(&plans).Error; err != nil {
		response.InternalErrorf(c, "SERVER_ERROR", "Unable to retrieve plans", err,
			"op", "db.subscription_plans.list_public")
		return
	}

	response.OK(c, "Plans retrieved", gin.H{"plans": plans})
}
