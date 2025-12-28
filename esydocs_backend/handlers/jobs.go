package handlers

import (
	"encoding/json"
	"esydocs_backend_go/database"
	"esydocs_backend_go/processing"
	"fmt"
	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
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
	// Ensure uploads directory exists
	if err := os.MkdirAll("uploads", os.ModePerm); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload directory"})
		return
	}

	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse form"})
		return
	}

	files := form.File["files"]
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}

	toolType := form.Value["toolType"][0]
	if toolType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Tool type is required"})
		return
	}
	
	options := ""
	if len(form.Value["options"]) > 0 {
		options = form.Value["options"][0]
	}

	var totalSize int64
	var originalFileName string
	var inputPaths []string

	if toolType == "merge-pdf" {
		originalFileName = "merged.pdf"
	} else {
		originalFileName = files[0].Filename
	}

	for _, file := range files {
		totalSize += file.Size
		dst := filepath.Join("uploads", file.Filename)
		if err := c.SaveUploadedFile(file, dst); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save uploaded file"})
			return
		}
		inputPaths = append(inputPaths, dst)
	}

	job := database.ProcessingJob{
		FileName: originalFileName,
		FileSize: fmt.Sprintf("%.2f KB", float64(totalSize)/1024),
		ToolType: toolType,
		Status:   "pending",
		Metadata: datatypes.JSON(fmt.Sprintf(`{"inputPaths": "%v", "options": %s}`, inputPaths, options)),
	}

	if err := database.DB.Create(&job).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create job"})
		return
	}

	// Asynchronous processing
	go processing.ProcessFile(job.ID, toolType, inputPaths, options)

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
	case "pdf-to-powerpoint":
		fileName = strings.Replace(fileName, ".pdf", ".pptx", 1)
		contentType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
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
