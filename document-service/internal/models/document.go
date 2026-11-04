// Package models owns the document library schema: documents, the folders and
// tags that organize them, saved exports, and job workspace hints.
//
// Rows are scoped to a user and optionally to an organization, and deletion is
// two-phase — DeletedAt marks a document as trashed so it can be restored,
// while a separate purge removes it permanently along with its stored object.
// Callers must therefore filter on the soft-delete state; forgetting to do so
// surfaces trashed documents in the library.
package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// DocumentStatus values.
const (
	StatusUploaded   = "uploaded"
	StatusProcessing = "processing"
	StatusReady      = "ready"
	StatusFailed     = "failed"
)

// Document is a persistent, owned, searchable file in a user's library. The
// bytes live in object storage (StoragePath); only metadata lives here.
type Document struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID         uuid.UUID  `gorm:"type:uuid;not null;index:idx_doc_user_created,priority:1" json:"userId"`
	OrganizationID *uuid.UUID `gorm:"type:uuid" json:"organizationId,omitempty"`
	FolderID       *uuid.UUID `gorm:"type:uuid;index" json:"folderId,omitempty"`
	Name           string     `gorm:"type:text;not null" json:"name"`
	FileType       string     `gorm:"type:text" json:"fileType,omitempty"`
	MimeType       string     `gorm:"type:text" json:"mimeType,omitempty"`
	FileSize       int64      `gorm:"default:0" json:"fileSize"`
	StoragePath    string     `gorm:"type:text" json:"-"`
	ThumbnailPath  string     `gorm:"type:text" json:"thumbnailPath,omitempty"`
	Status         string     `gorm:"type:text;not null;default:'uploaded'" json:"status"`
	// SourceJobID links a document created from a completed processing job. A
	// partial unique index on (user_id, source_job_id) makes finalize idempotent.
	SourceJobID      *uuid.UUID     `gorm:"type:uuid" json:"-"`
	ExtractedContent string         `gorm:"type:text" json:"-"`
	Metadata         datatypes.JSON `gorm:"type:jsonb" json:"metadata,omitempty"`
	// DocType is the ai-service extract classification a client saved (see
	// handlers/extraction.go for the allowlist, mirrored by a CHECK constraint).
	DocType *string `gorm:"type:text" json:"docType,omitempty"`
	// Extraction is the saved extract result. Always nil for id_document: ID
	// values are never persisted server-side (ai-service PII posture).
	Extraction datatypes.JSON `gorm:"type:jsonb" json:"extraction,omitempty"`
	// ContentSHA256 is computed by this service from the stored object (lowercase
	// hex); never accepted from a client, so exact-duplicate matches can't be forged.
	ContentSHA256 *string `gorm:"column:content_sha256;type:text" json:"-"`
	// SimHash is the client-computed 64-bit text fingerprint (stored signed; the
	// wire form is 16 hex chars) for near-duplicate detection.
	SimHash     *int64         `gorm:"column:simhash" json:"-"`
	UploadedAt  *time.Time     `json:"uploadedAt,omitempty"`
	ProcessedAt *time.Time     `json:"processedAt,omitempty"`
	CreatedAt   time.Time      `gorm:"index:idx_doc_user_created,priority:2;default:CURRENT_TIMESTAMP" json:"createdAt"`
	UpdatedAt   time.Time      `gorm:"default:CURRENT_TIMESTAMP" json:"updatedAt"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
	Tags        []Tag          `gorm:"many2many:document_tags;" json:"tags,omitempty"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (d *Document) BeforeCreate(tx *gorm.DB) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}
