package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

	outputFileName := fmt.Sprintf("processed_%s_%d", jobID, time.Now().Unix())
	var outputPath string
	var err error

	switch toolType {
	case "merge-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = mergePDFs(inputPaths, outputPath)
	case "split-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".zip")
		mode, _ := optionString(options, "mode")
		rangeValue, _ := optionString(options, "range")
		err = splitPDF(inputPaths[0], outputPath, mode, rangeValue)
	case "remove-pages":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		pages, ok := optionString(options, "pages")
		if !ok {
			return Result{}, fmt.Errorf("missing pages option")
		}
		err = removePages(inputPaths[0], outputPath, pages)
	case "extract-pages":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		pages, ok := optionString(options, "pages")
		if !ok {
			return Result{}, fmt.Errorf("missing pages option")
		}
		err = extractPages(inputPaths[0], outputPath, pages)
	case "organize-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		order, ok := optionString(options, "order")
		if !ok {
			return Result{}, fmt.Errorf("missing order option")
		}
		err = organizePDF(inputPaths[0], outputPath, order)
	case "rotate-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		rotationStr, ok := optionString(options, "rotation")
		if !ok {
			return Result{}, fmt.Errorf("missing rotation option")
		}
		rotation, convErr := strconv.Atoi(rotationStr)
		if convErr != nil || (rotation != 90 && rotation != 180 && rotation != 270) {
			return Result{}, fmt.Errorf("invalid rotation value: must be 90, 180, or 270")
		}
		applyTo, _ := optionString(options, "applyToPages")
		err = rotatePDF(inputPaths[0], outputPath, rotation, applyTo)
	case "scan-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = scanToPDF(ctx, inputPaths, outputPath, options)
	case "watermark-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = watermarkPDF(inputPaths[0], outputPath, options)
	case "protect-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		password, ok := optionString(options, "password")
		if !ok {
			return Result{}, fmt.Errorf("missing password option")
		}
		err = encryptPDF(inputPaths[0], outputPath, password)
	case "unlock-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		password, ok := optionString(options, "password")
		if !ok {
			return Result{}, fmt.Errorf("missing password option for decryption")
		}
		err = decryptPDF(inputPaths[0], outputPath, password)
	case "sign-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = signPDF(inputPaths[0], outputPath, options)
	case "edit-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = editPDF(inputPaths[0], outputPath, options)
	case "add-page-numbers":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = addPageNumbers(inputPaths[0], outputPath, options)
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
