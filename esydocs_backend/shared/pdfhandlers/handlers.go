package pdfhandlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"esydocs/shared/database"
)

type OutputMapping struct {
	Extension   string
	ContentType string
}

type HandlerConfig struct {
	SupportedTools map[string]bool
	Normalizations map[string]string
	OutputMappings map[string]OutputMapping
	DB             *gorm.DB
}

type Handlers struct {
	tools          map[string]bool
	normalizations map[string]string
	outputMappings map[string]OutputMapping
	db             *gorm.DB
}

func NewHandlers(cfg HandlerConfig) *Handlers {
	return &Handlers{
		tools:          cfg.SupportedTools,
		normalizations: cfg.Normalizations,
		outputMappings: cfg.OutputMappings,
		db:             cfg.DB,
	}
}

func (h *Handlers) normalizeToolType(toolType string) string {
	toolType = strings.TrimSpace(toolType)
	if mapped, ok := h.normalizations[toolType]; ok {
		return mapped
	}
	return toolType
}

func (h *Handlers) toolFromParam(c *gin.Context) (string, error) {
	toolType := h.normalizeToolType(c.Param("tool"))
	if toolType == "" {
		return "", fmt.Errorf("tool is required")
	}
	if h.tools != nil && !h.tools[toolType] {
		return "", fmt.Errorf("unsupported tool")
	}
	return toolType, nil
}

func (h *Handlers) GetJobs(c *gin.Context) {
	var jobs []database.ProcessingJob
	toolType, err := h.toolFromParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.db.Where("tool_type = ?", toolType).Find(&jobs).Error; err != nil {
		slog.Error("failed to fetch jobs", "tool", toolType, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch jobs"})
		return
	}
	c.JSON(http.StatusOK, jobs)
}

func (h *Handlers) GetJob(c *gin.Context) {
	toolType, err := h.toolFromParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	var job database.ProcessingJob
	if err := h.db.First(&job, "id = ? AND tool_type = ?", id, toolType).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handlers) CreateJob(c *gin.Context) {
	req, err := h.parseJobRequest(c, nil, "")
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "failed to create upload directory" || err.Error() == "failed to save uploaded file" {
			status = http.StatusInternalServerError
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	job, err := h.createJob(req)
	if err != nil {
		slog.Error("failed to create job", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, job)
}

func (h *Handlers) UpdateJob(c *gin.Context) {
	toolType, err := h.toolFromParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	var job database.ProcessingJob
	if err := h.db.First(&job, "id = ? AND tool_type = ?", id, toolType).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	var updateData struct {
		Status   *string `json:"status"`
		Progress *int    `json:"progress"`
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

	if err := h.db.Save(&job).Error; err != nil {
		slog.Error("failed to update job", "jobId", id, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update job"})
		return
	}

	c.JSON(http.StatusOK, job)
}

func (h *Handlers) DeleteJob(c *gin.Context) {
	toolType, err := h.toolFromParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	var job database.ProcessingJob
	if err := h.db.First(&job, "id = ? AND tool_type = ?", id, toolType).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	if err := h.db.Delete(&job).Error; err != nil {
		slog.Error("failed to delete job", "jobId", id, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete job"})
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handlers) DownloadFile(c *gin.Context) {
	toolType, err := h.toolFromParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id := c.Param("id")
	var job database.ProcessingJob
	if err := h.db.First(&job, "id = ? AND tool_type = ?", id, toolType).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	if job.Status != "completed" {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not ready for download"})
		return
	}

	var meta map[string]interface{}
	if err := json.Unmarshal(job.Metadata, &meta); err != nil {
		slog.Error("failed to unmarshal job metadata", "jobId", id, "error", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Processed file not found"})
		return
	}
	outputFilePath, ok := meta["outputFilePath"].(string)
	if !ok || outputFilePath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Processed file not found"})
		return
	}

	contentType := "application/octet-stream"
	fileName := job.FileName

	if mapping, ok := h.outputMappings[job.ToolType]; ok {
		fileName = strings.TrimSuffix(fileName, filepath.Ext(fileName)) + mapping.Extension
		contentType = mapping.ContentType
	} else {
		contentType = "application/pdf"
	}

	// Fix #6: Use mime.FormatMediaType for safe Content-Disposition
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fileName}))
	c.Header("Content-Type", contentType)
	c.File(outputFilePath)

	go func() {
		time.Sleep(1 * time.Hour)
		if err := os.Remove(outputFilePath); err != nil {
			slog.Error("failed to cleanup output file", "path", outputFilePath, "error", err)
		}
		if inputPaths, ok := meta["inputPaths"].([]interface{}); ok {
			for _, p := range inputPaths {
				if path, ok := p.(string); ok {
					if err := os.Remove(path); err != nil {
						slog.Error("failed to cleanup input file", "path", path, "error", err)
					}
				}
			}
		}
	}()
}

func (h *Handlers) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/:tool/jobs", h.CreateJob)
	group.GET("/:tool/jobs", h.GetJobs)
	group.GET("/:tool/jobs/:id", h.GetJob)
	group.PUT("/:tool/jobs/:id", h.UpdateJob)
	group.DELETE("/:tool/jobs/:id", h.DeleteJob)
	group.GET("/:tool/jobs/:id/download", h.DownloadFile)
}
