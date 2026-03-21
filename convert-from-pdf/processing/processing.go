package processing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type Result struct {
	OutputPath string
	Metadata   map[string]interface{}
}

func ProcessFile(ctx context.Context, jobID uuid.UUID, toolType string, inputPaths []string, options map[string]interface{}, outputDir string, onProgress ProgressFunc) (Result, error) {
	if outputDir == "" {
		outputDir = "outputs"
	}
	if len(inputPaths) == 0 {
		return Result{}, fmt.Errorf("no input files provided")
	}
	if err := os.MkdirAll(outputDir, 0750); err != nil {
		return Result{}, fmt.Errorf("failed to create output directory: %w", err)
	}

	outputFileName := fmt.Sprintf("processed_%s_%d", jobID, time.Now().Unix())
	var outputPath string
	var err error

	switch toolType {
	case "pdf-to-image", "pdf-to-img":
		outputPath, err = pdfToImages(ctx, inputPaths[0], outputDir, outputFileName)
	case "pdf-to-pdfa":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = pdfToPdfa(ctx, inputPaths[0], outputPath)
	case "pdf-to-word", "pdf-to-docx":
		outputPath = filepath.Join(outputDir, outputFileName+".docx")
		err = pdfToOfficeTicking(ctx, inputPaths[0], outputPath, "docx", onProgress)
	case "pdf-to-excel", "pdf-to-xlsx":
		outputPath = filepath.Join(outputDir, outputFileName+".xlsx")
		err = pdfToOfficeTicking(ctx, inputPaths[0], outputPath, "xlsx", onProgress)
	case "pdf-to-ppt", "pdf-to-powerpoint", "pdf-to-pptx":
		outputPath = filepath.Join(outputDir, outputFileName+".pptx")
		err = pdfToPptImages(ctx, inputPaths[0], outputPath, onProgress)
	case "pdf-to-html":
		outputPath = filepath.Join(outputDir, outputFileName+".zip")
		err = pdfToHTML(ctx, inputPaths[0], outputPath)
	case "pdf-to-text", "pdf-to-txt":
		outputPath = filepath.Join(outputDir, outputFileName+".txt")
		err = pdfToText(ctx, inputPaths[0], outputPath)
	default:
		err = fmt.Errorf("unsupported tool type: %s", toolType)
	}

	if err != nil {
		return Result{}, err
	}

	meta := map[string]interface{}{}
	// Store the actual output extension so downstream services (job-service)
	// can determine the correct Content-Type and download filename.
	if ext := filepath.Ext(outputPath); ext != "" {
		meta["outputExt"] = ext
	}
	return Result{
		OutputPath: outputPath,
		Metadata:   meta,
	}, nil
}
