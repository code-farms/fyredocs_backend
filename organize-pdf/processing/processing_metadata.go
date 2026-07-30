package processing

import (
	"fmt"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
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

// setPDFMetadata writes the requested fields into the PDF Info dictionary via
// pdfcpu's properties API. pdfcpu v0.9.1 has no dedicated SetInfo call, but
// PropertiesAdd writes the given keys verbatim into the Info dict and the
// writer only regenerates Producer/CreationDate/ModDate — so the standard
// Title/Author/Subject/Keywords entries survive intact. AddKeywordsFile is
// deliberately NOT used: it appends to existing keywords, which is the wrong
// semantic for "set the metadata".
func setPDFMetadata(inputPath, outputPath string, options map[string]interface{}) error {
	props, err := parseMetadataOptions(options)
	if err != nil {
		return err
	}
	if err := api.AddPropertiesFile(inputPath, outputPath, props, model.NewDefaultConfiguration()); err != nil {
		return fmt.Errorf("set pdf metadata: %w", err)
	}
	return nil
}
