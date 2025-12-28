package handlers

import (
	"convert-from-pdf/processing"
	"github.com/gin-gonic/gin"
	"net/http"
)

var pdfFromTools = map[string]bool{
	"pdf-to-word":       true,
	"pdf-to-excel":      true,
	"pdf-to-powerpoint": true,
	"pdf-to-ppt":        true,
	"pdf-to-image":      true,
	"pdf-to-img":        true,
}

func CreatePdfFromJob(c *gin.Context) {
	req, err := parseJobRequest(c, pdfFromTools)
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
