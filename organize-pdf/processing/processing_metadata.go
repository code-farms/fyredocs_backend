package processing

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// pdf-metadata: writes Title / Author / Subject / Keywords into the output
// PDF's Info dictionary.

// maxMetadataFieldLen mirrors the cap enforced at the public boundary in
// job-service/handlers/tool_options.go (duplicated by design — no shared
// business logic across services). The worker re-clamps defensively: values
// land verbatim inside the PDF, so an oversized field would bloat the file.
const maxMetadataFieldLen = 512

// metadataInfoKeys maps the lowercase option keys onto the exact PDF Info
// dictionary key names. NOTE: the option shape (title/author/subject/keywords)
// is validated in job-service/handlers/tool_options.go — keep both in sync.
var metadataInfoKeys = map[string]string{
	"title":    "Title",
	"author":   "Author",
	"subject":  "Subject",
	"keywords": "Keywords",
}

// parseMetadataOptions extracts the Info-dict entries to write, trimming and
// length-capping each. Errors only when no usable field remains — an empty
// metadata job has nothing to do and failing early beats emitting a byte-for-
// byte copy that looks like success.
func parseMetadataOptions(options map[string]interface{}) (map[string]string, error) {
	props := make(map[string]string, len(metadataInfoKeys))
	for optionKey, infoKey := range metadataInfoKeys {
		value, ok := optionString(options, optionKey)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > maxMetadataFieldLen {
			trimmed = trimmed[:maxMetadataFieldLen]
		}
		props[infoKey] = trimmed
	}
	if len(props) == 0 {
		return nil, fmt.Errorf("pdf-metadata requires at least one of: title, author, subject, keywords")
	}
	return props, nil
}

// setPDFMetadata sets the requested Info dictionary fields, replacing any
// existing values, in one read and one write.
//
// pdfcpu v0.16 reserves "Keywords" in its properties API (validate.DocumentProperty)
// and keeps keywords as a set that the writer rebuilds into the Info entry, so
// writing Keywords as a property is rejected. Keywords therefore go through the
// keywords API, but its Add call alone would merge with keywords already in the
// file; removing them all first gives "set" semantics. The user's keywords string
// is stored as one keyword so it lands in the Info entry verbatim, as before.
// Title/Author/Subject still go through PropertiesAdd. The api.*File wrappers are
// not used because each would read and rewrite the whole file again.
func setPDFMetadata(ctx context.Context, inputPath, outputPath string, options map[string]interface{}) (err error) {
	props, err := parseMetadataOptions(options)
	if err != nil {
		return err
	}
	keywords, setKeywords := props["Keywords"]
	delete(props, "Keywords")

	in, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("set pdf metadata: open input: %w", err)
	}
	defer in.Close()

	conf := model.NewDefaultConfiguration()
	conf.Cmd = model.ADDPROPERTIES
	pdfCtx, err := api.ReadValidateAndOptimize(ctx, in, conf, nil)
	if err != nil {
		return fmt.Errorf("set pdf metadata: read: %w", err)
	}
	if len(props) > 0 {
		if err := pdfcpu.PropertiesAdd(ctx, pdfCtx, props); err != nil {
			return fmt.Errorf("set pdf metadata: properties: %w", err)
		}
	}
	if setKeywords {
		if _, err := pdfcpu.KeywordsRemove(ctx, pdfCtx, nil); err != nil {
			return fmt.Errorf("set pdf metadata: clear keywords: %w", err)
		}
		if err := pdfcpu.KeywordsAdd(ctx, pdfCtx, []string{keywords}); err != nil {
			return fmt.Errorf("set pdf metadata: keywords: %w", err)
		}
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("set pdf metadata: create output: %w", err)
	}
	defer func() {
		if cerr := out.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("set pdf metadata: close output: %w", cerr)
		}
		if err != nil {
			_ = os.Remove(outputPath)
		}
	}()
	if err := api.WriteContext(ctx, pdfCtx, out); err != nil {
		return fmt.Errorf("set pdf metadata: write: %w", err)
	}
	return nil
}
