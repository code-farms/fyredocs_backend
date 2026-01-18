package handlers

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

var convertFromTools = map[string]bool{
	"pdf-to-image":      true,
	"pdf-to-word":       true,
	"pdf-to-powerpoint": true,
	"pdf-to-excel":      true,
	"pdf-to-pdfa":       true,
}

func CreatePdfFromJob(c *gin.Context) {
	req, err := parseJobRequest(c, convertFromTools, c.Param("tool"))
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
