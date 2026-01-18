package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"optimize-pdf/database"
)

func GetJobs(c *gin.Context) {
	var jobs []database.ProcessingJob
	toolType, err := toolFromParam(c, optimizeTools)
	if err != nil {
		respondJobError(c, err, http.StatusBadRequest)
		return
	}

	if err := database.DB.Where("tool_type = ?", toolType).Find(&jobs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch jobs"})
		return
	}
	c.JSON(http.StatusOK, jobs)
}

func GetJob(c *gin.Context) {
	toolType, err := toolFromParam(c, optimizeTools)
	if err != nil {
		respondJobError(c, err, http.StatusBadRequest)
		return
	}

	id := c.Param("id")
	var job database.ProcessingJob
	if err := database.DB.First(&job, "id = ? AND tool_type = ?", id, toolType).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func UpdateJob(c *gin.Context) {
	toolType, err := toolFromParam(c, optimizeTools)
	if err != nil {
		respondJobError(c, err, http.StatusBadRequest)
		return
	}

	id := c.Param("id")
	var job database.ProcessingJob
	if err := database.DB.First(&job, "id = ? AND tool_type = ?", id, toolType).Error; err != nil {
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

	if updateData.Status != nil && *updateData.Status == "completed" {
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
	toolType, err := toolFromParam(c, optimizeTools)
	if err != nil {
		respondJobError(c, err, http.StatusBadRequest)
		return
	}

	id := c.Param("id")
	var job database.ProcessingJob
	if err := database.DB.First(&job, "id = ? AND tool_type = ?", id, toolType).Error; err != nil {
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
	toolType, err := toolFromParam(c, optimizeTools)
	if err != nil {
		respondJobError(c, err, http.StatusBadRequest)
		return
	}

	id := c.Param("id")
	var job database.ProcessingJob
	if err := database.DB.First(&job, "id = ? AND tool_type = ?", id, toolType).Error; err != nil {
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

	contentType := "application/pdf"
	fileName := job.FileName
	if !strings.HasSuffix(fileName, ".pdf") {
		fileName = strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".pdf"
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
