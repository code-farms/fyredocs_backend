package processing

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

func splitPDF(inputPath string, outputPath string, pageRange string) error {
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
		err := api.ExtractPagesFile(inputPath, outputFile, []string{fmt.Sprintf("%d", i)}, nil)
		if err != nil {
			slog.Warn("failed to extract page", "page", i, "error", err)
			continue
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

func officeToPDF(ctx context.Context, inputPath string, outputPath string, fileType string) error {
	slog.Info("converting to PDF", "type", fileType, "input", inputPath)

	outputDir := filepath.Dir(outputPath)

	cmd := exec.CommandContext(ctx, "libreoffice", "--headless", "--convert-to", "pdf", "--outdir", outputDir, inputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("libreoffice conversion failed", "output", string(output))
		return fmt.Errorf("LibreOffice not available or conversion failed: %w", err)
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

func watermarkPDF(inputPath string, outputPath string, watermarkText string) error {
	slog.Info("adding watermark to PDF", "input", inputPath)

	wm, err := pdfcpu.ParseTextWatermarkDetails(watermarkText, "font:Helvetica, points:48, color:0.5 0.5 0.5, opacity:0.3, rotation:45", false, types.POINTS)
	if err != nil {
		return fmt.Errorf("failed to parse watermark: %w", err)
	}

	return api.AddWatermarksFile(inputPath, outputPath, nil, wm, nil)
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
