package processing

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// encryptPDF applies password protection. The password is a user secret and must
// never be logged or echoed into an error message.
func encryptPDF(inputPath string, outputPath string, password string) error {
	slog.Info("encrypting PDF", "input", inputPath)
	conf := model.NewDefaultConfiguration()
	conf.UserPW = password
	conf.OwnerPW = password
	return api.EncryptFile(inputPath, outputPath, conf)
}

// decryptPDF removes password protection, given the correct password. A wrong
// password surfaces as a tool error; the message must not distinguish "wrong
// password" from "not encrypted" in a way that helps guessing.
func decryptPDF(inputPath string, outputPath string, password string) error {
	slog.Info("decrypting PDF", "input", inputPath)
	conf := model.NewDefaultConfiguration()
	conf.UserPW = password
	return api.DecryptFile(inputPath, outputPath, conf)
}

// watermarkPDF dispatches to the text or image watermark implementation based on
// the supplied options.
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

// watermarkPDFText stamps rendered text across pages. Option values reach a
// pdfcpu description string, so they are constrained rather than interpolated
// freely — see hexToRGBFloat for the colour case.
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

// watermarkPDFImage stamps a user-supplied image across pages. The image is
// itself an untrusted upload, so it is handled as a file path rather than being
// decoded here.
// decodeDataURLImage writes a data-URL image ("data:image/png;base64,…") into
// dir and returns the file path. The extension comes from the mime prefix so
// the filename and pdfcpu's content sniffing agree — signPDF previously wrote
// every payload as .png, silently mislabeling JPEG signatures. webp is only
// offered where the caller's pdfcpu path accepts it (watermarks).
func decodeDataURLImage(dataURL, dir, baseName string, allowWebp bool) (string, error) {
	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid image data format")
	}
	imgBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("failed to decode image data: %w", err)
	}
	ext := ".png"
	if strings.Contains(parts[0], "jpeg") || strings.Contains(parts[0], "jpg") {
		ext = ".jpg"
	} else if allowWebp && strings.Contains(parts[0], "webp") {
		ext = ".webp"
	}
	path := filepath.Join(dir, baseName+ext)
	if err := os.WriteFile(path, imgBytes, 0600); err != nil {
		return "", fmt.Errorf("failed to write image: %w", err)
	}
	return path, nil
}

func watermarkPDFImage(inputPath, outputPath string, options map[string]interface{}, position string, opacity float64, rotation int) error {
	imageData, ok := optionString(options, "imageData")
	if !ok || imageData == "" {
		return fmt.Errorf("missing watermark image data")
	}

	tempDir, err := os.MkdirTemp("", "pdf-watermark-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	imgPath, err := decodeDataURLImage(imageData, tempDir, "watermark", true)
	if err != nil {
		return fmt.Errorf("watermark image: %w", err)
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

// hexToRGBFloat converts a #RRGGBB colour to the 0..1 float triple pdfcpu
// expects.
//
// This is also a sanitization step: the result is formatted into a pdfcpu
// description string, so parsing to numbers first means a crafted colour value
// cannot inject extra description directives. Unparseable input must therefore
// fall back to a default rather than being passed through.
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

// addPageNumbers stamps page numbers using the same description-string mechanism
// as the watermark tools, with position and format taken from options.
func addPageNumbers(inputPath string, outputPath string, options map[string]interface{}) error {
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

	posMap := map[string]string{
		"bc": "bc", "bl": "bl", "br": "br",
		"tc": "tc", "tl": "tl", "tr": "tr",
		"c": "c",
	}
	pdfcpuPos, ok := posMap[position]
	if !ok {
		pdfcpuPos = "bc"
	}

	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}
	pageCount := ctx.PageCount

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
	}

	slog.Info("page numbers added", "pages", pageCount, "format", format)
	return nil
}

// signatureWatermarkDesc builds the pdfcpu watermark description for placing a
// signature image. It uses the unambiguous "scalefactor" key — the abbreviated
// "sc" prefix is rejected by pdfcpu as ambiguous ("ambiguous parameter prefix").
func signatureWatermarkDesc(pdfcpuPos string) string {
	return fmt.Sprintf("pos:%s, scalefactor:0.25, rot:0, opacity:1", pdfcpuPos)
}

// signPDF stamps a signature image onto a chosen page and position.
//
// This is a visual signature only — it does not create a cryptographic PDF
// signature, so it proves nothing about authenticity. The distinction matters if
// anyone reads the tool name as offering more than it does.
func signPDF(inputPath string, outputPath string, options map[string]interface{}) error {
	slog.Info("signing PDF", "input", inputPath)

	position := "br"
	if pos, ok := options["position"].(string); ok && pos != "" {
		position = pos
	}

	page := -1
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

	tempDir, err := os.MkdirTemp("", "pdf-sign-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// png/jpeg only (no webp): job-service's validateSignOptions enforces the
	// same prefix set at the public boundary — defence in depth.
	sigPath, err := decodeDataURLImage(signatureData, tempDir, "signature", false)
	if err != nil {
		return fmt.Errorf("signature image: %w", err)
	}

	ctx, err := api.ReadContextFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read PDF: %w", err)
	}

	targetPage := page
	if targetPage <= 0 {
		targetPage = ctx.PageCount
	}

	posMap := map[string]string{
		"bc": "bc", "bl": "bl", "br": "br",
		"tc": "tc", "tl": "tl", "tr": "tr",
		"c": "c",
	}
	pdfcpuPos, ok := posMap[position]
	if !ok {
		pdfcpuPos = "br"
	}

	desc := signatureWatermarkDesc(pdfcpuPos)

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

// editPDF applies a combined edit description (annotations and stamps) in one
// pass, so a multi-part edit produces a single output rather than chaining tools.
func editPDF(inputPath string, outputPath string, options map[string]interface{}) error {
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
	}

	slog.Info("PDF edited", "annotationCount", len(annotations))
	return nil
}
