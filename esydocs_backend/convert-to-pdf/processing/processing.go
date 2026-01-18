package processing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
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

type convertAPIError struct {
	statusCode int
	message    string
}

func (e convertAPIError) Error() string {
	return fmt.Sprintf("convertapi status=%d: %s", e.statusCode, e.message)
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
		err = callConvertAPI("pdf", "docx", inputPaths, outputPath, nil)
	case "pdf-to-excel":
		outputPath = filepath.Join(outputDir, outputFileName+".xlsx")
		err = callConvertAPI("pdf", "xlsx", inputPaths, outputPath, nil)
	case "pdf-to-powerpoint", "pdf-to-ppt":
		outputPath = filepath.Join(outputDir, outputFileName+".pptx")
		err = callConvertAPI("pdf", "pptx", inputPaths, outputPath, nil)
	case "pdf-to-image", "pdf-to-img":
		outputPath = filepath.Join(outputDir, outputFileName+".zip")
		err = callConvertAPI("pdf", "jpg", inputPaths, outputPath, nil)
	case "word-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = callConvertAPI("docx", "pdf", inputPaths, outputPath, nil)
	case "ppt-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = callConvertAPI("pptx", "pdf", inputPaths, outputPath, nil)
	case "excel-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = callConvertAPI("xlsx", "pdf", inputPaths, outputPath, nil)
	case "image-to-pdf", "img-to-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		imageTool, imageErr := imageToolFromPath(inputPaths[0])
		if imageErr != nil {
			err = imageErr
			break
		}
		err = callConvertAPI(imageTool, "pdf", inputPaths, outputPath, nil)
	case "compress-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = callConvertAPI("pdf", "compress", inputPaths, outputPath, map[string]string{"StoreFile": "true"})
	case "merge-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = callConvertAPI("pdf", "merge", inputPaths, outputPath, nil)
	case "split-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".zip")
		rangeValue, ok := optionString(options, "range")
		if !ok {
			return Result{}, fmt.Errorf("missing range option")
		}
		err = callConvertAPI("pdf", "split", inputPaths, outputPath, map[string]string{"PageRange": rangeValue})
	case "protect-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		password, ok := optionString(options, "password")
		if !ok {
			return Result{}, fmt.Errorf("missing password option")
		}
		err = callConvertAPI("pdf", "encrypt", inputPaths, outputPath, map[string]string{"UserPassword": password})
	case "edit-pdf", "unlock-pdf", "sign-pdf", "watermark-pdf":
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

func callConvertAPI(tool string, conversionType string, inputPaths []string, outputPath string, apiParams map[string]string) error {
	apiKey := convertAPISecret()
	if apiKey == "" {
		return fmt.Errorf("CONVERT_API_SECRET is not set")
	}
	url := fmt.Sprintf("https://v2.convertapi.com/convert/%s/to/%s", tool, conversionType)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for _, path := range inputPaths {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		part, err := writer.CreateFormFile("Files", filepath.Base(path))
		if err != nil {
			return err
		}
		if _, err := io.Copy(part, file); err != nil {
			return err
		}
	}

	for key, val := range apiParams {
		_ = writer.WriteField(key, val)
	}
	_ = writer.WriteField("Secret", apiKey)

	writer.Close()

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		if isRecoverableError(err) {
			return err
		}
		return fmt.Errorf("convertapi request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return convertAPIError{statusCode: resp.StatusCode, message: string(respBody)}
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func convertAPISecret() string {
	if value := cleanSecret(os.Getenv("CONVERT_API_SECRET")); value != "" {
		return value
	}
	if value := cleanSecret(os.Getenv("CONVERT_API_KEY")); value != "" {
		return value
	}
	if value := cleanSecret(os.Getenv("CONVERT_API_TOKEN")); value != "" {
		return value
	}
	return ""
}

func cleanSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.Trim(value, "\"'")
}

func isRecoverableError(err error) bool {
	if err == nil {
		return false
	}
	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout() || netErr.Temporary()
	}
	return false
}

func imageToolFromPath(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return "jpg", nil
	case ".png":
		return "png", nil
	case ".webp":
		return "webp", nil
	default:
		return "", fmt.Errorf("unsupported image type: %s", ext)
	}
}
