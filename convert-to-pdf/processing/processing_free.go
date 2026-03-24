package processing

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func mergePDFs(inputPaths []string, outputPath string) error {
	if len(inputPaths) < 2 {
		return fmt.Errorf("merge requires at least 2 PDF files")
	}
	slog.Info("merging PDFs", "count", len(inputPaths), "output", outputPath)
	return api.MergeCreateFile(inputPaths, outputPath, false, nil)
}

func splitPDF(inputPath string, outputPath string, pageRange string, onProgress ProgressFunc) error {
	slog.Info("splitting PDF", "input", inputPath, "range", pageRange)

	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	pageCount := ctx.PageCount
	slog.Info("PDF page count", "pages", pageCount)

	tempDir, err := os.MkdirTemp("", "pdf-split-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	for i := 1; i <= pageCount; i++ {
		outputFile := filepath.Join(tempDir, fmt.Sprintf("page_%03d.pdf", i))
		err := api.CollectFile(inputPath, outputFile, []string{fmt.Sprintf("%d", i)}, nil)
		if err != nil {
			slog.Warn("failed to extract page", "page", i, "error", err)
			continue
		}
		if onProgress != nil {
			onProgress(i, pageCount)
		}
	}

	return zipDirectory(tempDir, outputPath)
}

func compressPDF(inputPath string, outputPath string) error {
	slog.Info("compressing PDF", "input", inputPath)
	conf := model.NewDefaultConfiguration()
	conf.OptimizeDuplicateContentStreams = true
	return api.OptimizeFile(inputPath, outputPath, conf)
}

func encryptPDF(inputPath string, outputPath string, password string) error {
	slog.Info("encrypting PDF", "input", inputPath)
	conf := model.NewDefaultConfiguration()
	conf.UserPW = password
	conf.OwnerPW = password
	return api.EncryptFile(inputPath, outputPath, conf)
}

func decryptPDF(inputPath string, outputPath string, password string) error {
	slog.Info("decrypting PDF", "input", inputPath)
	conf := model.NewDefaultConfiguration()
	conf.UserPW = password
	return api.DecryptFile(inputPath, outputPath, conf)
}

// unoserver connection settings (configurable via environment variables).
var (
	unoHost = envOrDefault("UNOSERVER_HOST", "127.0.0.1")
	unoPort = envOrDefault("UNOSERVER_PORT", "2002")
)

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func officeToPDF(ctx context.Context, inputPath string, outputPath string, fileType string) error {
	slog.Info("converting to PDF", "type", fileType, "input", inputPath)

	// Fast path: unoconvert via persistent LibreOffice daemon.
	// Use a 30s timeout so hung unoconvert falls back faster.
	unoCtx, unoCancel := context.WithTimeout(ctx, 30*time.Second)
	defer unoCancel()
	cmd := exec.CommandContext(unoCtx, "unoconvert",
		"--host", unoHost, "--port", unoPort,
		"--convert-to", "pdf",
		inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	slog.Warn("unoconvert failed, falling back to direct libreoffice",
		"output", string(output), "error", err)

	// Slow fallback: spawn a fresh LibreOffice process.
	outputDir := filepath.Dir(outputPath)
	cmd = exec.CommandContext(ctx, "libreoffice", "--headless", "--convert-to", "pdf", "--outdir", outputDir, inputPath)
	output, err = cmd.CombinedOutput()
	if err != nil {
		slog.Error("libreoffice fallback failed", "output", string(output))
		return fmt.Errorf("conversion failed: %w", err)
	}

	convertedFile := filepath.Join(outputDir, strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))+".pdf")
	if convertedFile != outputPath {
		return os.Rename(convertedFile, outputPath)
	}
	return nil
}

func imageToPDF(inputPaths []string, outputPath string) error {
	slog.Info("converting images to PDF", "count", len(inputPaths))
	return api.ImportImagesFile(inputPaths, outputPath, nil, nil)
}

func watermarkPDF(inputPath string, outputPath string, options map[string]interface{}) error {
	slog.Info("adding watermark to PDF", "input", inputPath)

	wmType, _ := optionString(options, "type")
	if wmType == "" {
		wmType = "text"
	}

	position, _ := optionString(options, "position")
	if position == "" {
		position = "diagonal"
	}

	opacity := 0.3
	if op, ok := options["opacity"]; ok {
		switch v := op.(type) {
		case float64:
			opacity = v / 100.0
		case string:
			if parsed, err := strconv.ParseFloat(v, 64); err == nil {
				opacity = parsed / 100.0
			}
		}
	}

	rotation := 0
	switch position {
	case "diagonal":
		rotation = 45
	case "center":
		rotation = 0
	case "tiled":
		rotation = 45
	}

	if wmType == "image" {
		return watermarkPDFImage(inputPath, outputPath, options, position, opacity, rotation)
	}
	return watermarkPDFText(inputPath, outputPath, options, position, opacity, rotation)
}

func watermarkPDFText(inputPath, outputPath string, options map[string]interface{}, position string, opacity float64, rotation int) error {
	text, _ := optionString(options, "text")
	if text == "" {
		text = "CONFIDENTIAL"
	}

	fontSize := 48
	if fs, ok := options["fontSize"]; ok {
		switch v := fs.(type) {
		case float64:
			fontSize = int(v)
		case string:
			if parsed, err := strconv.Atoi(v); err == nil {
				fontSize = parsed
			}
		}
	}

	colorStr, _ := optionString(options, "color")
	r, g, b := hexToRGBFloat(colorStr)

	desc := fmt.Sprintf("font:Helvetica, points:%d, color:%s %s %s, opacity:%.2f, rotation:%d",
		fontSize, r, g, b, opacity, rotation)

	wm, err := pdfcpu.ParseTextWatermarkDetails(text, desc, false, types.POINTS)
	if err != nil {
		return fmt.Errorf("failed to parse text watermark: %w", err)
	}

	return api.AddWatermarksFile(inputPath, outputPath, nil, wm, nil)
}

func watermarkPDFImage(inputPath, outputPath string, options map[string]interface{}, position string, opacity float64, rotation int) error {
	imageData, ok := optionString(options, "imageData")
	if !ok || imageData == "" {
		return fmt.Errorf("missing watermark image data")
	}

	parts := strings.SplitN(imageData, ",", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid watermark image data format")
	}
	imgBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("failed to decode watermark image: %w", err)
	}

	ext := ".png"
	if strings.Contains(parts[0], "jpeg") || strings.Contains(parts[0], "jpg") {
		ext = ".jpg"
	} else if strings.Contains(parts[0], "webp") {
		ext = ".webp"
	}

	tempDir, err := os.MkdirTemp("", "pdf-watermark-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	imgPath := filepath.Join(tempDir, "watermark"+ext)
	if err := os.WriteFile(imgPath, imgBytes, 0600); err != nil {
		return fmt.Errorf("failed to write watermark image: %w", err)
	}

	scale := 0.3
	if s, ok := options["scale"]; ok {
		switch v := s.(type) {
		case float64:
			scale = v / 100.0
		case string:
			if parsed, err := strconv.ParseFloat(v, 64); err == nil {
				scale = parsed / 100.0
			}
		}
	}

	desc := fmt.Sprintf("scalefactor:%.2f, opacity:%.2f, rot:%d", scale, opacity, rotation)

	wm, err := pdfcpu.ParseImageWatermarkDetails(imgPath, desc, false, types.POINTS)
	if err != nil {
		return fmt.Errorf("failed to parse image watermark: %w", err)
	}

	return api.AddWatermarksFile(inputPath, outputPath, nil, wm, nil)
}

func hexToRGBFloat(hex string) (string, string, string) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return "0.5", "0.5", "0.5"
	}
	r, err1 := strconv.ParseUint(hex[0:2], 16, 8)
	g, err2 := strconv.ParseUint(hex[2:4], 16, 8)
	b, err3 := strconv.ParseUint(hex[4:6], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return "0.5", "0.5", "0.5"
	}
	return fmt.Sprintf("%.3f", float64(r)/255.0),
		fmt.Sprintf("%.3f", float64(g)/255.0),
		fmt.Sprintf("%.3f", float64(b)/255.0)
}

func addPageNumbers(inputPath string, outputPath string, options map[string]interface{}, onProgress ProgressFunc) error {
	slog.Info("adding page numbers to PDF", "input", inputPath)

	position := "bc"
	if pos, ok := options["position"].(string); ok && pos != "" {
		position = pos
	}

	fontSize := 12
	if fs, ok := options["fontSize"]; ok {
		switch v := fs.(type) {
		case float64:
			fontSize = int(v)
		case string:
			if parsed, err := strconv.Atoi(v); err == nil {
				fontSize = parsed
			}
		}
	}

	startNumber := 1
	if sn, ok := options["startNumber"]; ok {
		switch v := sn.(type) {
		case float64:
			startNumber = int(v)
		case string:
			if parsed, err := strconv.Atoi(v); err == nil {
				startNumber = parsed
			}
		}
	}

	format := "{n}"
	if f, ok := options["format"].(string); ok && f != "" {
		format = f
	}

	// Map short position codes to pdfcpu position strings
	posMap := map[string]string{
		"bc": "bc", "bl": "bl", "br": "br",
		"tc": "tc", "tl": "tl", "tr": "tr",
		"c": "c",
	}
	pdfcpuPos, ok := posMap[position]
	if !ok {
		pdfcpuPos = "bc"
	}

	// Read page count
	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}
	pageCount := ctx.PageCount

	// Copy input to output first, then stamp page by page
	if err := copyFile(inputPath, outputPath); err != nil {
		return fmt.Errorf("failed to copy input file: %w", err)
	}

	for i := 1; i <= pageCount; i++ {
		pageNum := startNumber + i - 1
		totalPages := startNumber + pageCount - 1

		text := format
		text = strings.ReplaceAll(text, "{n}", strconv.Itoa(pageNum))
		text = strings.ReplaceAll(text, "{total}", strconv.Itoa(totalPages))

		desc := fmt.Sprintf("font:Helvetica, points:%d, pos:%s, scale:1 abs, rot:0, color:0 0 0, opacity:1", fontSize, pdfcpuPos)

		wm, err := pdfcpu.ParseTextWatermarkDetails(text, desc, true, types.POINTS)
		if err != nil {
			return fmt.Errorf("failed to parse page number stamp for page %d: %w", i, err)
		}

		pageSelection := []string{strconv.Itoa(i)}
		if err := api.AddWatermarksFile(outputPath, "", pageSelection, wm, nil); err != nil {
			return fmt.Errorf("failed to add page number to page %d: %w", i, err)
		}
		if onProgress != nil {
			onProgress(i, pageCount)
		}
	}

	slog.Info("page numbers added", "pages", pageCount, "format", format)
	return nil
}

func signPDF(inputPath string, outputPath string, options map[string]interface{}) error {
	slog.Info("signing PDF", "input", inputPath)

	position := "br"
	if pos, ok := options["position"].(string); ok && pos != "" {
		position = pos
	}

	page := -1 // -1 means last page
	if p, ok := options["page"]; ok {
		switch v := p.(type) {
		case float64:
			page = int(v)
		case string:
			if parsed, err := strconv.Atoi(v); err == nil {
				page = parsed
			}
		}
	}

	signatureData, ok := options["signatureData"].(string)
	if !ok || signatureData == "" {
		return fmt.Errorf("missing signature data")
	}

	// Decode base64 signature image (data URL format: data:image/png;base64,...)
	parts := strings.SplitN(signatureData, ",", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid signature data format")
	}
	imgBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("failed to decode signature image: %w", err)
	}

	// Write signature to temp file
	tempDir, err := os.MkdirTemp("", "pdf-sign-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	sigPath := filepath.Join(tempDir, "signature.png")
	if err := os.WriteFile(sigPath, imgBytes, 0600); err != nil {
		return fmt.Errorf("failed to write signature image: %w", err)
	}

	// Read page count for "last page" handling
	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	targetPage := page
	if targetPage <= 0 {
		targetPage = ctx.PageCount
	}

	// Map short position codes to pdfcpu position strings
	posMap := map[string]string{
		"bc": "bc", "bl": "bl", "br": "br",
		"tc": "tc", "tl": "tl", "tr": "tr",
		"c": "c",
	}
	pdfcpuPos, ok := posMap[position]
	if !ok {
		pdfcpuPos = "br"
	}

	desc := fmt.Sprintf("pos:%s, sc:.25, rot:0, opacity:1", pdfcpuPos)

	wm, err := pdfcpu.ParseImageWatermarkDetails(sigPath, desc, true, types.POINTS)
	if err != nil {
		return fmt.Errorf("failed to parse signature watermark: %w", err)
	}

	pageSelection := []string{strconv.Itoa(targetPage)}
	if err := api.AddWatermarksFile(inputPath, outputPath, pageSelection, wm, nil); err != nil {
		return fmt.Errorf("failed to add signature: %w", err)
	}

	slog.Info("PDF signed", "page", targetPage, "position", position)
	return nil
}

func editPDF(inputPath string, outputPath string, options map[string]interface{}, onProgress ProgressFunc) error {
	slog.Info("editing PDF with annotations", "input", inputPath)

	annotationsRaw, ok := options["annotations"]
	if !ok {
		return fmt.Errorf("missing annotations option")
	}

	annotationsJSON, err := json.Marshal(annotationsRaw)
	if err != nil {
		return fmt.Errorf("failed to marshal annotations: %w", err)
	}

	var annotations []struct {
		Type     string `json:"type"`
		Content  string `json:"content"`
		Page     int    `json:"page"`
		Position string `json:"position"`
		FontSize int    `json:"fontSize"`
	}
	if err := json.Unmarshal(annotationsJSON, &annotations); err != nil {
		return fmt.Errorf("failed to parse annotations: %w", err)
	}

	if len(annotations) == 0 {
		return fmt.Errorf("no annotations provided")
	}

	// Copy input to output first, then apply stamps
	if err := copyFile(inputPath, outputPath); err != nil {
		return fmt.Errorf("failed to copy input file: %w", err)
	}

	posMap := map[string]string{
		"bc": "bc", "bl": "bl", "br": "br",
		"tc": "tc", "tl": "tl", "tr": "tr",
		"c": "c",
	}

	for i, ann := range annotations {
		if ann.Type != "text" {
			continue
		}

		fontSize := ann.FontSize
		if fontSize <= 0 {
			fontSize = 12
		}

		pdfcpuPos, ok := posMap[ann.Position]
		if !ok {
			pdfcpuPos = "bc"
		}

		desc := fmt.Sprintf("font:Helvetica, points:%d, pos:%s, scale:1 abs, rot:0, color:0 0 0, opacity:1", fontSize, pdfcpuPos)

		wm, err := pdfcpu.ParseTextWatermarkDetails(ann.Content, desc, true, types.POINTS)
		if err != nil {
			return fmt.Errorf("failed to parse annotation %d: %w", i, err)
		}

		pageSelection := []string{strconv.Itoa(ann.Page)}
		if err := api.AddWatermarksFile(outputPath, "", pageSelection, wm, nil); err != nil {
			return fmt.Errorf("failed to add annotation %d: %w", i, err)
		}
		if onProgress != nil {
			onProgress(i+1, len(annotations))
		}
	}

	slog.Info("PDF edited", "annotationCount", len(annotations))
	return nil
}

func zipDirectory(sourceDir string, zipPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("failed to create zip: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		zipEntry, err := zipWriter.Create(relPath)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(zipEntry, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
