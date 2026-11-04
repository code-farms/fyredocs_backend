package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/bits"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	"document-service/internal/models"
	"fyredocs/shared/response"
)

// docTypes is the ai-service extract classification set.
//
// NOTE: mirrors DOC_TYPES in ai-service/internal/extract/schema.go and the
// chk_document_doc_type constraint in migrations/00004 — this service keeps
// its own copy by design (no cross-service imports, CLAUDE.md §1).
var docTypes = map[string]bool{
	"invoice": true, "receipt": true, "id_document": true, "business_card": true,
	"form": true, "letter": true, "contract": true, "statement": true, "other": true,
}

const (
	// maxExtractionBytes caps a saved extraction. ai-service bounds its own
	// output (≤10 tables × 200 rows × 20 cols), so a real result fits easily;
	// the cap stops the column becoming a general blob store.
	maxExtractionBytes = 64 << 10
	// nearDuplicateDistance is the SimHash Hamming threshold for "re-scan of",
	// matching NEAR_DUP_BITS in fyredocs_app/src/lib/dedupe/simhash.ts.
	nearDuplicateDistance = 3
	// maxNearCandidates bounds the near-duplicate scan of one scope.
	maxNearCandidates = 5000
	// maxDuplicates caps each list in the duplicates response.
	maxDuplicates = 10
)

var errIDExtraction = errors.New("id_document extractions are not stored")

// validDocType reports whether t is a known extract type. Empty is invalid:
// callers that allow "no type" check for nil before calling.
func validDocType(t string) bool { return docTypes[t] }

// parseSimHash reads the client wire form — 16 lowercase or uppercase hex
// chars, as fyredocs_app's simhash() emits — into the signed column value.
// Signed storage is a bit-for-bit reinterpretation; Hamming distance is
// unaffected.
func parseSimHash(hex string) (int64, error) {
	hex = strings.TrimSpace(hex)
	if len(hex) != 16 {
		return 0, errors.New("simhash must be 16 hex characters")
	}
	u, err := strconv.ParseUint(hex, 16, 64)
	if err != nil {
		return 0, errors.New("simhash must be 16 hex characters")
	}
	return int64(u), nil
}

// hamming is the number of differing bits between two fingerprints.
func hamming(a, b int64) int { return bits.OnesCount64(uint64(a ^ b)) }

// normalizeExtraction validates a client extraction for storage: a JSON object
// within maxExtractionBytes, and nothing at all for id_document — which returns
// errIDExtraction so the caller can say why. A nil/empty raw means "no
// extraction" and is valid.
func normalizeExtraction(docType string, raw json.RawMessage) (datatypes.JSON, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if docType == "id_document" {
		return nil, errIDExtraction
	}
	if len(trimmed) > maxExtractionBytes {
		return nil, errors.New("extraction is too large")
	}
	var obj map[string]any
	if err := json.Unmarshal(trimmed, &obj); err != nil || obj == nil {
		return nil, errors.New("extraction must be a JSON object")
	}
	return datatypes.JSON(trimmed), nil
}

// putExtractionReq is the PUT /documents/:id/extraction body.
type putExtractionReq struct {
	DocType    string          `json:"docType" binding:"required"`
	Extraction json.RawMessage `json:"extraction"`
}

// PutExtraction saves the AI extract result for a document — what the web
// reader's Details card and the app's backup send. Replaces any previous one.
// Editor+ in an org scope, like any other document write.
func PutExtraction(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidID, "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "editor")
	if !ok {
		return
	}
	var req putExtractionReq
	if !response.BindJSON(c, &req) {
		return
	}
	docType := strings.TrimSpace(req.DocType)
	if !validDocType(docType) {
		response.BadRequest(c, response.CodeInvalidInput, "docType is not a known document type.")
		return
	}
	extraction, err := normalizeExtraction(docType, req.Extraction)
	if err != nil {
		msg := err.Error() + "."
		if errors.Is(err, errIDExtraction) {
			msg = "ID document details are not stored on the server — send docType only."
		}
		response.BadRequest(c, response.CodeInvalidInput, msg)
		return
	}

	res := scopeOwned(models.DB.Model(&models.Document{}), uid, orgID).Where("id = ?", id).
		Updates(map[string]any{"doc_type": docType, "extraction": extraction})
	if res.Error != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not save the document details.", res.Error,
			"op", "db.documents.extraction", "docId", id)
		return
	}
	if res.RowsAffected == 0 {
		response.NotFound(c, response.CodeNotFound, "Document not found.")
		return
	}
	response.OK(c, "Document details saved", gin.H{"id": id, "docType": docType})
}

// DuplicateRef is one match in the duplicates response.
type DuplicateRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// Distance is the SimHash Hamming distance (near matches only).
	Distance *int `json:"distance,omitempty"`
}

// duplicatesResult is the GET /documents/:id/duplicates payload. Pending is
// true while this document's content hash has not been computed yet, so an
// empty Exact list means "unknown", not "none".
type duplicatesResult struct {
	Exact   []DuplicateRef `json:"exact"`
	Near    []DuplicateRef `json:"near"`
	Pending bool           `json:"pending"`
}

type candidate struct {
	ID      uuid.UUID
	Name    string
	SimHash *int64 `gorm:"column:simhash"`
}

// nearMatches keeps candidates within nearDuplicateDistance of target, nearest
// first, excluding the ids in skip (the document itself and exact matches).
func nearMatches(target int64, cands []candidate, skip map[uuid.UUID]bool) []DuplicateRef {
	var out []DuplicateRef
	for _, cand := range cands {
		if cand.SimHash == nil || skip[cand.ID] {
			continue
		}
		if d := hamming(target, *cand.SimHash); d <= nearDuplicateDistance {
			dist := d
			out = append(out, DuplicateRef{ID: cand.ID, Name: cand.Name, Distance: &dist})
		}
	}
	// Insertion sort: lists are tiny (≤ maxDuplicates after the cap below).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && *out[j].Distance < *out[j-1].Distance; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) > maxDuplicates {
		out = out[:maxDuplicates]
	}
	return out
}

// GetDuplicates lists other live documents in the same scope that are the same
// file (content SHA-256) or a re-scan of it (SimHash within 3 bits). A
// document without a hash yet gets one computed in the background.
func GetDuplicates(c *gin.Context) {
	uid, _ := userID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.CodeInvalidID, "Invalid document id.")
		return
	}
	orgID, ok := resolveOrg(c, c.Query("orgId"), "viewer")
	if !ok {
		return
	}
	var doc models.Document
	if err := scopeOwned(models.DB, uid, orgID).Where("id = ?", id).First(&doc).Error; err != nil {
		response.NotFound(c, response.CodeNotFound, "Document not found.")
		return
	}

	result := duplicatesResult{Exact: []DuplicateRef{}, Near: []DuplicateRef{}}
	skip := map[uuid.UUID]bool{doc.ID: true}
	if doc.ContentSHA256 != nil {
		var exact []candidate
		if err := scopeOwned(models.DB.Model(&models.Document{}), uid, orgID).
			Select("id, name, simhash").
			Where("content_sha256 = ? AND id <> ?", *doc.ContentSHA256, doc.ID).
			Order("created_at DESC").Limit(maxDuplicates).Scan(&exact).Error; err != nil {
			response.InternalErrorf(c, response.CodeServerError, "Could not check for duplicates.", err,
				"op", "db.documents.duplicates", "docId", id)
			return
		}
		for _, e := range exact {
			result.Exact = append(result.Exact, DuplicateRef{ID: e.ID, Name: e.Name})
			skip[e.ID] = true
		}
	} else {
		result.Pending = doc.StoragePath != ""
		HashDocumentAsync(doc.ID, doc.StoragePath)
	}

	if doc.SimHash != nil {
		var cands []candidate
		if err := scopeOwned(models.DB.Model(&models.Document{}), uid, orgID).
			Select("id, name, simhash").
			Where("simhash IS NOT NULL AND id <> ?", doc.ID).
			Order("created_at DESC").Limit(maxNearCandidates).Scan(&cands).Error; err != nil {
			response.InternalErrorf(c, response.CodeServerError, "Could not check for duplicates.", err,
				"op", "db.documents.near_duplicates", "docId", id)
			return
		}
		result.Near = nearMatches(*doc.SimHash, cands, skip)
		if result.Near == nil {
			result.Near = []DuplicateRef{}
		}
	}
	response.OK(c, "Duplicates checked", result)
}

// parseAIFields validates the optional AI fields on create: docType (allowlist),
// extraction (needs a docType; refused for id_document) and the 16-hex SimHash.
// msg is a user-facing sentence when invalid.
func parseAIFields(docType *string, extraction json.RawMessage, simhash *string) (*string, datatypes.JSON, *int64, string) {
	var dt *string
	if docType != nil && strings.TrimSpace(*docType) != "" {
		t := strings.TrimSpace(*docType)
		if !validDocType(t) {
			return nil, nil, nil, "docType is not a known document type."
		}
		dt = &t
	}
	var ex datatypes.JSON
	if len(bytes.TrimSpace(extraction)) > 0 && !bytes.Equal(bytes.TrimSpace(extraction), []byte("null")) {
		if dt == nil {
			return nil, nil, nil, "extraction needs a docType."
		}
		var err error
		if ex, err = normalizeExtraction(*dt, extraction); err != nil {
			if errors.Is(err, errIDExtraction) {
				return nil, nil, nil, "ID document details are not stored on the server — send docType only."
			}
			return nil, nil, nil, err.Error() + "."
		}
	}
	var sh *int64
	if simhash != nil && strings.TrimSpace(*simhash) != "" {
		v, err := parseSimHash(*simhash)
		if err != nil {
			return nil, nil, nil, err.Error() + "."
		}
		sh = &v
	}
	return dt, ex, sh, ""
}
