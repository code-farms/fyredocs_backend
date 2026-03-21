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

type Result struct {
	OutputPath string
	Metadata   map[string]interface{}
}

func ProcessFile(ctx context.Context, jobID uuid.UUID, toolType string, inputPaths []string, options map[string]interface{}, outputDir string) (Result, error) {
	if outputDir == "" {
		outputDir = "outputs"
	}
	if len(inputPaths) == 0 {
		return Result{}, fmt.Errorf("no input files provided")
	}
	if err := os.MkdirAll(outputDir, 0750); err != nil {
		return Result{}, fmt.Errorf("failed to create output directory: %w", err)
	}

	outputFileName := fmt.Sprintf("optimized_%s_%d", jobID, time.Now().Unix())
	var outputPath string
	var err error
	var metadata map[string]interface{}

	switch toolType {
	case "compress-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		metadata, err = compressPDF(ctx, inputPaths[0], outputPath, options)
	case "repair-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = repairPDF(ctx, inputPaths[0], outputPath)
	case "ocr-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		metadata, err = ocrPDF(ctx, inputPaths[0], outputPath, options)
	default:
		err = fmt.Errorf("unsupported tool type: %s", toolType)
	}

	if err != nil {
		return Result{}, err
	}

	meta := map[string]interface{}{}
	for k, v := range metadata {
		meta[k] = v
	}

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
