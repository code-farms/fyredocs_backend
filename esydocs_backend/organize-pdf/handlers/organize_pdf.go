package handlers

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

var organizeTools = map[string]bool{
	"merge-pdf":     true,
	"split-pdf":     true,
	"remove-pages":  true,
	"extract-pages": true,
	"organize-pdf":  true,
	"scan-to-pdf":   true,
}

func CreateOrganizePdfJob(c *gin.Context) {
	req, err := parseJobRequest(c, organizeTools, c.Param("tool"))
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
