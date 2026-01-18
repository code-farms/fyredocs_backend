package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

var optimizeTools = map[string]bool{
	"compress-pdf": true,
	"repair-pdf":   true,
	"ocr-pdf":      true,
}

func CreateOptimizePdfJob(c *gin.Context) {
	req, err := parseJobRequest(c, optimizeTools, c.Param("tool"))
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "failed to create upload directory" || err.Error() == "failed to save uploaded file" {
			status = http.StatusInternalServerError
		}
		respondJobError(c, err, status)
		return
	}

	job, err := createJob(c, req)
	if err != nil {
		respondJobError(c, err, http.StatusInternalServerError)
		return
	}

	processJobAsync(job.ID, req.ToolType, req.Files, req.Options)

	c.JSON(http.StatusCreated, job)
}
