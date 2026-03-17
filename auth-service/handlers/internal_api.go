package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"auth-service/internal/models"

	"esydocs/shared/response"
)

// GetUserPlan returns the subscription plan for a user.
// This is an internal API endpoint for service-to-service calls.
// GET /internal/users/:id/plan
func GetUserPlan(c *gin.Context) {
	idStr := c.Param("id")
	parsedID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", "Invalid user ID")
		return
	}

	var user models.User
	if err := models.DB.First(&user, "id = ?", parsedID).Error; err != nil {
		response.Err(c, http.StatusNotFound, "NOT_FOUND", "User not found")
		return
	}

	var plan models.SubscriptionPlan
	if err := models.DB.Where("name = ?", user.PlanName).First(&plan).Error; err != nil {
		plan = models.SubscriptionPlan{Name: "free", MaxFileSizeMB: 25, MaxFilesPerJob: 10, RetentionDays: 7}
	}

	response.OK(c, "User plan retrieved", gin.H{
		"userId": user.ID.String(),
		"plan": gin.H{
			"name":           plan.Name,
			"maxFileSizeMb":  plan.MaxFileSizeMB,
			"maxFilesPerJob": plan.MaxFilesPerJob,
			"retentionDays":  plan.RetentionDays,
		},
	})
}
