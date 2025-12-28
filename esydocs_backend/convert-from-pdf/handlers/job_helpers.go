package handlers

import (
	"convert-from-pdf/database"
	"fmt"
	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"os"
	"path/filepath"
)

type jobRequest struct {
	ToolType string
	Options  string
	Files    []string
	SizeKB   string
	FileName string
}

func parseJobRequest(c *gin.Context, allowedTools map[string]bool) (*jobRequest, error) {
	if err := os.MkdirAll("uploads", os.ModePerm); err != nil {
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

	toolType := ""
	if len(form.Value["toolType"]) > 0 {
		toolType = form.Value["toolType"][0]
	}
	if toolType == "" {
		return nil, fmt.Errorf("tool type is required")
	}
	if allowedTools != nil && !allowedTools[toolType] {
		return nil, fmt.Errorf("unsupported tool type")
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
		SizeKB:   fmt.Sprintf("%.2f KB", float64(totalSize)/1024),
		FileName: originalFileName,
	}, nil
}

func createJob(c *gin.Context, req *jobRequest) (*database.ProcessingJob, error) {
	job := database.ProcessingJob{
		FileName: req.FileName,
		FileSize: req.SizeKB,
		ToolType: req.ToolType,
		Status:   "pending",
		Metadata: datatypes.JSON(fmt.Sprintf(`{"inputPaths": "%v", "options": %s}`, req.Files, req.Options)),
	}

	if err := database.DB.Create(&job).Error; err != nil {
		return nil, fmt.Errorf("failed to create job")
	}

	return &job, nil
}

func respondJobError(c *gin.Context, err error, status int) {
	c.JSON(status, gin.H{"error": err.Error()})
}
