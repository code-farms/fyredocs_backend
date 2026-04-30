package handlers

import (
	"auth-service/internal/models"

	"fyredocs/shared/response"

	"github.com/gin-gonic/gin"
)

// GetAllPlans returns all public subscription plans and their limits.
// GET /auth/plans — no authentication required.
func GetAllPlans(c *gin.Context) {
	var plans []models.SubscriptionPlan
	if err := models.DB.
		Where("name != ?", "anonymous").
		Order("max_file_size_mb asc").
		Find(&plans).Error; err != nil {
		response.InternalError(c, "SERVER_ERROR", "Unable to retrieve plans")
		return
	}

	response.OK(c, "Plans retrieved", gin.H{"plans": plans})
}
