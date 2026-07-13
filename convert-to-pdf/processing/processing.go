// Package processing performs the convert-to-pdf conversions: it turns office
// documents, images, and HTML into PDF (and the related ODF conversions),
// dispatching each tool type to the appropriate converter.
package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Result is the output of a conversion: the produced file's path and any
// metadata to surface on the job.
type Result struct {
	OutputPath string
	Metadata   map[string]interface{}
}

// ProgressFunc is called by processing functions to report real progress.
// current is the current item (1-based), total is the total number of items.
type ProgressFunc func(current, total int)

// ProcessFile converts the given inputs for toolType and writes the result into
// outputDir, reporting progress via onProgress. It validates inputs and routes
// to the tool-specific converter.
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
	case "word-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = officeToPDF(ctx, inputPaths[0], outputPath, "docx")
	case "ppt-to-pdf", "powerpoint-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = officeToPDF(ctx, inputPaths[0], outputPath, "pptx")
	case "excel-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = officeToPDF(ctx, inputPaths[0], outputPath, "xlsx")
	case "html-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = officeToPDF(ctx, inputPaths[0], outputPath, "html")
	case "image-to-pdf", "img-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = imageToPDF(inputPaths, outputPath)
	case "odt-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = officeToPDF(ctx, inputPaths[0], outputPath, "odt")
	case "ods-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = officeToPDF(ctx, inputPaths[0], outputPath, "ods")
	case "odp-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = officeToPDF(ctx, inputPaths[0], outputPath, "odp")
	case "word-to-odt":
		outputPath = filepath.Join(outputDir, outputFileName+".odt")
		err = officeToOffice(ctx, inputPaths[0], outputPath, "odt")
	case "excel-to-ods":
		outputPath = filepath.Join(outputDir, outputFileName+".ods")
		err = officeToOffice(ctx, inputPaths[0], outputPath, "ods")
	case "powerpoint-to-odp":
		outputPath = filepath.Join(outputDir, outputFileName+".odp")
		err = officeToOffice(ctx, inputPaths[0], outputPath, "odp")
	default:
		err = fmt.Errorf("unsupported tool type: %s", toolType)
	}

	if err != nil {
		return Result{}, err
	}

	meta := map[string]interface{}{}
	return Result{
		OutputPath: outputPath,
		Metadata:   meta,
	}, nil
}

func optionString(options map[string]interface{}, key string) (string, bool) {
	if options == nil {
		return "", false
	}
	value, ok := options[key]
	if !ok {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return typed, typed != ""
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return "", false
		}
		return strings.Trim(string(data), "\""), true
	}
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if e := out.Close(); e != nil {
			err = e
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
