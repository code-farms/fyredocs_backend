package processing

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"convert-to-pdf/database"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nguyenthenguyen/docx"
	"gorm.io/datatypes"
)

const outputDir = "outputs"

// updateJobStatus updates the status and progress of a job
func updateJobStatus(id uuid.UUID, status string, progress string, metadataUpdates ...map[string]interface{}) {
	var job database.ProcessingJob
	if err := database.DB.First(&job, "id = ?", id).Error; err != nil {
		fmt.Println("Error finding job to update:", err)
		return
	}

	job.Status = status
	job.Progress = progress

	if status == "completed" {
		now := time.Now()
		job.CompletedAt = &now
	}

	// Merge metadata
	if len(metadataUpdates) > 0 {
		var existingMeta map[string]interface{}
		json.Unmarshal(job.Metadata, &existingMeta)
		if existingMeta == nil {
			existingMeta = make(map[string]interface{})
		}
		for _, newMeta := range metadataUpdates {
			for k, v := range newMeta {
				existingMeta[k] = v
			}
		}
		newMetaBytes, _ := json.Marshal(existingMeta)
		job.Metadata = datatypes.JSON(newMetaBytes)
	}

	database.DB.Save(&job)
}

func ProcessFile(jobID uuid.UUID, toolType string, inputPaths []string, options string) {
	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		fmt.Println("Failed to create output directory:", err)
		updateJobStatus(jobID, "failed", "0")
		return
	}

	updateJobStatus(jobID, "processing", "25")

	outputFileName := fmt.Sprintf("processed_%s_%d", jobID, time.Now().Unix())
	var outputPath string
	var err error

	// Parse options
	var opts map[string]interface{}
	if options != "" {
		json.Unmarshal([]byte(options), &opts)
	}

	switch toolType {
	case "pdf-to-word":
		outputPath = filepath.Join(outputDir, outputFileName+".docx")
		err = convertPdfToWord(inputPaths[0], outputPath)
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
	case "ppt-to-pdf", "powerpoint-to-pdf":
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
		err = callConvertAPI("pdf", "split", inputPaths, outputPath, map[string]string{"PageRange": opts["range"].(string)})
	case "protect-pdf":
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = callConvertAPI("pdf", "encrypt", inputPaths, outputPath, map[string]string{"UserPassword": opts["password"].(string)})
	case "edit-pdf", "unlock-pdf", "sign-pdf", "watermark-pdf":
		// Placeholder: copy file
		outputPath = filepath.Join(outputDir, outputFileName+".pdf")
		err = copyFile(inputPaths[0], outputPath)
	default:
		err = fmt.Errorf("unsupported tool type: %s", toolType)
	}

	if err != nil {
		fmt.Printf("Processing failed for job %s: %v\n", jobID, err)
		updateJobStatus(jobID, "failed", "0")
		return
	}

	updateJobStatus(jobID, "processing", "75")

	// Final update
	finalMeta := map[string]interface{}{
		"outputFilePath": outputPath,
	}
	updateJobStatus(jobID, "completed", "100", finalMeta)

	fmt.Printf("Job %s completed successfully. Output at: %s\n", jobID, outputPath)
}

func convertPdfToWord(inputPath, outputPath string) error {
	// This is a mock conversion, similar to the original Node.js implementation
	r, err := docx.ReadDocxFile(inputPath)
	if err != nil {
		// This will fail for PDFs, which is the point of the mock.
		// Create a minimal docx with placeholder text.
		return createMinimalDocx(outputPath, "This is a placeholder document. The original file could not be read as a .docx.")
	}
	defer r.Close()

	// If it was a docx, just save it again
	editable := r.Editable()
	err = editable.WriteToFile(outputPath)
	return err
}

func createMinimalDocx(outputPath string, text string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	zipWriter := zip.NewWriter(f)
	defer zipWriter.Close()

	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`
	if err := writeZipEntry(zipWriter, "[Content_Types].xml", contentTypes); err != nil {
		return err
	}

	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`
	if err := writeZipEntry(zipWriter, "_rels/.rels", rels); err != nil {
		return err
	}

	docRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`
	if err := writeZipEntry(zipWriter, "word/_rels/document.xml.rels", docRels); err != nil {
		return err
	}

	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(text)); err != nil {
		return err
	}
	document := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>%s</w:t></w:r></w:p>
  </w:body>
</w:document>`, escaped.String())
	return writeZipEntry(zipWriter, "word/document.xml", document)
}

func writeZipEntry(zipWriter *zip.Writer, name string, contents string) error {
	writer, err := zipWriter.Create(name)
	if err != nil {
		return err
	}
	_, err = writer.Write([]byte(contents))
	return err
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer func() {
		if e := out.Close(); e != nil {
			err = e
		}
	}()

	_, err = io.Copy(out, in)
	if err != nil {
		return
	}

	err = out.Sync()
	return
}

func callConvertAPI(tool string, conversionType string, inputPaths []string, outputPath string, apiParams map[string]string) error {
	apiKey := os.Getenv("CONVERT_API_SECRET")
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
		io.Copy(part, file)
	}
	
	for key, val := range apiParams {
		_ = writer.WriteField(key, val)
	}

	writer.Close()

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read body for error message
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ConvertAPI failed with status %d: %s", resp.StatusCode, string(respBody))
	}
	
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()
	
	_, err = io.Copy(out, resp.Body)
	return err
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
