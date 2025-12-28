package handlers

import (
	"encoding/json"
	"convert-to-pdf/database"
	"convert-to-pdf/processing"
	"github.com/gin-gonic/gin"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func GetJobs(c *gin.Context) {
	var jobs []database.ProcessingJob
	if err := database.DB.Find(&jobs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch jobs"})
		return
	}
	c.JSON(http.StatusOK, jobs)
}

func GetJob(c *gin.Context) {
	id := c.Param("id")
	var job database.ProcessingJob
	if err := database.DB.First(&job, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}
c.JSON(http.StatusOK, job)
}

func CreateJob(c *gin.Context) {
	req, err := parseJobRequest(c, nil)
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


func UpdateJob(c *gin.Context) {
	id := c.Param("id")
	var job database.ProcessingJob
	if err := database.DB.First(&job, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	var updateData struct {
		Status   *string `json:"status"`
		Progress *string `json:"progress"`
	}

	if err := c.ShouldBindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if updateData.Status != nil {
		job.Status = *updateData.Status
	}
	if updateData.Progress != nil {
		job.Progress = *updateData.Progress
	}
	
	if *updateData.Status == "completed" {
		now := time.Now()
		job.CompletedAt = &now
	}

	if err := database.DB.Save(&job).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update job"})
		return
	}

	c.JSON(http.StatusOK, job)
}

func DeleteJob(c *gin.Context) {
	id := c.Param("id")
	var job database.ProcessingJob
	if err := database.DB.First(&job, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	if err := database.DB.Delete(&job).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete job"})
		return
	}

	c.Status(http.StatusNoContent)
}

func DownloadFile(c *gin.Context) {
	id := c.Param("id")
	var job database.ProcessingJob
	if err := database.DB.First(&job, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	if job.Status != "completed" {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not ready for download"})
		return
	}

	var meta map[string]interface{}
	json.Unmarshal(job.Metadata, &meta)
	outputFilePath, ok := meta["outputFilePath"].(string)
	if !ok || outputFilePath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Processed file not found"})
		return
	}

	contentType := "application/octet-stream"
	fileName := job.FileName
	switch job.ToolType {
	case "pdf-to-word":
		fileName = strings.Replace(fileName, ".pdf", ".docx", 1)
		contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "pdf-to-excel":
		fileName = strings.Replace(fileName, ".pdf", ".xlsx", 1)
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "pdf-to-powerpoint", "pdf-to-ppt":
		fileName = strings.Replace(fileName, ".pdf", ".pptx", 1)
		contentType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case "pdf-to-image", "pdf-to-img":
		fileName = strings.Replace(fileName, ".pdf", ".zip", 1)
		contentType = "application/zip"
	case "ppt-to-pdf", "word-to-pdf", "excel-to-pdf", "image-to-pdf", "img-to-pdf":
		fileName = strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".pdf"
		contentType = "application/pdf"
	case "split-pdf":
		fileName = strings.Replace(fileName, ".pdf", ".zip", 1)
		contentType = "application/zip"
	default:
		contentType = "application/pdf"
	}

	c.Header("Content-Disposition", "attachment; filename="+fileName)
	c.Header("Content-Type", contentType)
	c.File(outputFilePath)

	// Cleanup
	go func() {
		time.Sleep(1 * time.Hour)
		os.Remove(outputFilePath)
		if inputPaths, ok := meta["inputPaths"].([]interface{}); ok {
			for _, p := range inputPaths {
				os.Remove(p.(string))
			}
		}
	}()
}
