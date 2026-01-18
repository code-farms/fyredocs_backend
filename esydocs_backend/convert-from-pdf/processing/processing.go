package processing

import (
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


func ProcessFile(jobID uuid.UUID, toolType string, inputPaths []string, options map[string]interface{}, outputDir string) (Result, error) {
	if outputDir == "" {
		outputDir = "outputs"
	}
	if len(inputPaths) == 0 {
		return Result{}, fmt.Errorf("no input files provided")
	}
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		return Result{}, fmt.Errorf("failed to create output directory: %w", err)
	}

	outputFileName := fmt.Sprintf("processed_%s_%d", jobID, time.Now().Unix())
	var outputPath string
	var err error

	switch toolType {
	case "pdf-to-image", "pdf-to-img":
		outputPath = filepath.Join(outputDir, outputFileName+".zip")
		err = pdfToImages(inputPaths[0], outputPath)
	case "pdf-to-pdfa":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = pdfToPdfa(inputPaths[0], outputPath)
	case "pdf-to-word", "pdf-to-docx":
		outputPath = filepath.Join(outputDir, outputFileName+".docx")
		err = pdfToOffice(inputPaths[0], outputPath, "docx")
	case "pdf-to-excel", "pdf-to-xlsx":
		outputPath = filepath.Join(outputDir, outputFileName+".xlsx")
		err = pdfToOffice(inputPaths[0], outputPath, "xlsx")
	case "pdf-to-ppt", "pdf-to-powerpoint", "pdf-to-pptx":
		outputPath = filepath.Join(outputDir, outputFileName+".pptx")
		err = pdfToOffice(inputPaths[0], outputPath, "pptx")
	case "pdf-to-html":
		outputPath = filepath.Join(outputDir, outputFileName+".zip")
		err = pdfToHTML(inputPaths[0], outputPath)
	case "pdf-to-text", "pdf-to-txt":
		outputPath = filepath.Join(outputDir, outputFileName+".txt")
		err = pdfToText(inputPaths[0], outputPath)
	default:
		err = fmt.Errorf("unsupported tool type: %s", toolType)
	}

	if err != nil {
		return Result{}, err
	}

	meta := map[string]interface{}{
		"outputFilePath": outputPath,
		"inputPaths":     inputPaths,
	}
	return Result{
		OutputPath: outputPath,
		Metadata:   meta,
	}, nil
}
