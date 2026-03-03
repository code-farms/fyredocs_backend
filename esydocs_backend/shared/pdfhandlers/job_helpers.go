package pdfhandlers

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"

	"esydocs/shared/database"
)

type jobRequest struct {
	ToolType string
	Options  string
	Files    []string
	FileSize int64
	FileName string
}

func (h *Handlers) parseJobRequest(c *gin.Context, allowedTools map[string]bool, toolTypeOverride string) (*jobRequest, error) {
	// Fix #17: Use 0750 instead of os.ModePerm
	if err := os.MkdirAll("uploads", 0750); err != nil {
		return nil, fmt.Errorf("failed to create upload directory")
	}

	form, err := c.MultipartForm()
	if err != nil {
		return nil, fmt.Errorf("failed to parse form")
	}

	files := form.File["files"]
	if len(files) == 0 {
		return nil, fmt.Errorf("no file uploaded")
	}

	toolType := strings.TrimSpace(toolTypeOverride)
	if toolType == "" && len(form.Value["toolType"]) > 0 {
		toolType = form.Value["toolType"][0]
	}
	toolType = h.normalizeToolType(strings.TrimSpace(toolType))
	if toolType == "" {
		return nil, fmt.Errorf("tool is required")
	}
	if allowedTools != nil && !allowedTools[toolType] {
		return nil, fmt.Errorf("unsupported tool")
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
			return nil, fmt.Errorf("failed to save uploaded file")
		}
		inputPaths = append(inputPaths, dst)
	}

	return &jobRequest{
		ToolType: toolType,
		Options:  options,
		Files:    inputPaths,
		FileSize: totalSize,
		FileName: originalFileName,
	}, nil
}

func (h *Handlers) createJob(req *jobRequest) (*database.ProcessingJob, error) {
	optionsJSON := "{}"
	if req.Options != "" {
		optionsJSON = req.Options
	}
	metadataStr := fmt.Sprintf(`{"inputPaths": %q, "options": %s}`, strings.Join(req.Files, ","), optionsJSON)

	job := database.ProcessingJob{
		FileName: req.FileName,
		FileSize: req.FileSize,
		ToolType: req.ToolType,
		Status:   "pending",
		Metadata: datatypes.JSON(metadataStr),
	}

	if err := h.db.Create(&job).Error; err != nil {
		slog.Error("failed to create job in database", "error", err)
		return nil, fmt.Errorf("failed to create job")
	}

	return &job, nil
}
