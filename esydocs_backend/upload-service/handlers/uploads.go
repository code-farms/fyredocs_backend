package handlers

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"os"
	"path/filepath"
)

type UploadedFile struct {
	OriginalName string `json:"originalName"`
	StoredPath   string `json:"storedPath"`
	Size         int64  `json:"size"`
}

func UploadFiles(c *gin.Context) {
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

	var uploaded []UploadedFile
	for _, file := range files {
		dst := filepath.Join("uploads", file.Filename)
		if err := c.SaveUploadedFile(file, dst); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save uploaded file"})
			return
		}
		uploaded = append(uploaded, UploadedFile{
			OriginalName: file.Filename,
			StoredPath:   dst,
			Size:         file.Size,
		})
	}

	c.JSON(http.StatusCreated, gin.H{"files": uploaded})
}
