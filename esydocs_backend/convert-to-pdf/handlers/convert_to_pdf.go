package handlers

import (
	"convert-to-pdf/processing"
	"github.com/gin-gonic/gin"
	"net/http"
)

var convertToTools = map[string]bool{
	"word-to-pdf":  true,
	"powerpoint-to-pdf": true,
	"excel-to-pdf": true,
	"image-to-pdf": true,
}

func CreatePdfToJob(c *gin.Context) {
	req, err := parseJobRequest(c, convertToTools, c.Param("tool"))
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

	go processing.ProcessFile(job.ID, req.ToolType, req.Files, req.Options)

	c.JSON(http.StatusCreated, job)
}
