package processing

import (
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
	case "pdf-to-word":
		outputPath = filepath.Join(outputDir, outputFileName+".docx")
		err = pdfToOffice(inputPaths[0], outputPath, "docx")
	case "pdf-to-excel":
		outputPath = filepath.Join(outputDir, outputFileName+".xlsx")
		err = pdfToOffice(inputPaths[0], outputPath, "xlsx")
	case "pdf-to-powerpoint", "pdf-to-ppt":
		outputPath = filepath.Join(outputDir, outputFileName+".pptx")
		err = pdfToOffice(inputPaths[0], outputPath, "pptx")
	case "pdf-to-image", "pdf-to-img":
		outputPath = filepath.Join(outputDir, outputFileName+".zip")
		err = pdfToImages(inputPaths[0], outputPath)
	case "word-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = officeToPDF(inputPaths[0], outputPath, "docx")
	case "ppt-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = officeToPDF(inputPaths[0], outputPath, "pptx")
	case "excel-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = officeToPDF(inputPaths[0], outputPath, "xlsx")
	case "html-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = officeToPDF(inputPaths[0], outputPath, "html")
	case "image-to-pdf", "img-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = imageToPDF(inputPaths, outputPath)
	case "compress-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = compressPDF(inputPaths[0], outputPath)
	case "merge-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = mergePDFs(inputPaths, outputPath)
	case "split-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".zip")
		rangeValue, ok := optionString(options, "range")
		if !ok {
			return Result{}, fmt.Errorf("missing range option")
		}
		err = splitPDF(inputPaths[0], outputPath, rangeValue)
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
	case "watermark-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		watermarkText, ok := optionString(options, "text")
		if !ok {
			watermarkText = "CONFIDENTIAL"
		}
		err = watermarkPDF(inputPaths[0], outputPath, watermarkText)
	case "edit-pdf", "sign-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = copyFile(inputPaths[0], outputPath)
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

// Legacy ConvertAPI functions removed - now using free open-source tools
// See processing_free.go for implementations using pdfcpu, LibreOffice, and Poppler
