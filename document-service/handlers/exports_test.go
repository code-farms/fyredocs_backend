// Tests for the export surface: request-validation guards (offline, nil DB —
// see harness_test.go) and the CSV renderer, whose output is fed to
// spreadsheet applications and therefore has an injection surface of its own.
package handlers

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"document-service/internal/models"
	"fyredocs/shared/response"
)

// TestCreateExportGuards covers CreateExport's pre-DB rejections and, via an
// org-scoped row, that a valid format is accepted case-insensitively (the
// request advances past the format gate to org resolution, where a non-member
// stub stops it before the insert).
func TestCreateExportGuards(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	post := func(body string) int {
		w := doAuthed(t, uid, http.MethodPost, "/api/exports", CreateExport, "/api/exports", body)
		return w.Code
	}

	t.Run("format validation", func(t *testing.T) {
		noUserService(t)
		for _, tt := range []struct {
			name, body, code string
		}{
			{"no body", "", response.CodeInvalidInput},
			{"missing format", `{}`, response.CodeInvalidInput},
			{"unsupported format", `{"format":"xml"}`, "INVALID_FORMAT"},
			{"non-uuid organizationId", `{"format":"csv","organizationId":"nope"}`, response.CodeInvalidInput},
			{"non-uuid folderId filter", `{"format":"csv","folderId":"nope"}`, response.CodeInvalidInput},
		} {
			t.Run(tt.name, func(t *testing.T) {
				w := doAuthed(t, uid, http.MethodPost, "/api/exports", CreateExport, "/api/exports", tt.body)
				wantErrEnvelope(t, w, http.StatusBadRequest, tt.code)
			})
		}
	})

	t.Run("uppercase CSV passes the format gate", func(t *testing.T) {
		// 404 (non-member) proves the request got PAST format validation into
		// resolveOrg — a case-sensitive regression would answer 400 instead.
		withUserService(t, membershipStatus(http.StatusNotFound))
		body := fmt.Sprintf(`{"format":"CSV","organizationId":"%s"}`, uuid.Must(uuid.NewV7()))
		if code := post(body); code != http.StatusNotFound {
			t.Errorf("org-scoped CSV export: status = %d, want 404 (format gate should have passed)", code)
		}
	})
}

// TestExportIDGuards: both :id export routes 400 a malformed identifier
// before any query runs.
func TestExportIDGuards(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	t.Run("get", func(t *testing.T) {
		w := doAuthed(t, uid, http.MethodGet, "/api/exports/:id", GetExport, "/api/exports/not-a-uuid", "")
		wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidID)
	})
	t.Run("download", func(t *testing.T) {
		w := doAuthed(t, uid, http.MethodGet, "/api/exports/:id/download", DownloadExport, "/api/exports/not-a-uuid/download", "")
		wantErrEnvelope(t, w, http.StatusBadRequest, response.CodeInvalidID)
	})
}

// TestDocsToCSV pins the CSV renderer: header shape, RFC4180 escaping of
// hostile characters in user-controlled names, and tag flattening. Rows are
// parsed back with encoding/csv, so what is asserted is what a conformant
// consumer would read.
func TestDocsToCSV(t *testing.T) {
	created := time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC)

	t.Run("empty library is header-only", func(t *testing.T) {
		rows := parseCSV(t, docsToCSV(nil))
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want header only", len(rows))
		}
		want := []string{"Name", "Type", "Size (bytes)", "Status", "Created", "Tags"}
		for i, col := range want {
			if rows[0][i] != col {
				t.Errorf("header[%d] = %q, want %q", i, rows[0][i], col)
			}
		}
	})

	t.Run("hostile names survive a round-trip", func(t *testing.T) {
		docs := []models.Document{
			{Name: `report, "final" version`, FileType: "pdf", FileSize: 1234, Status: "ready", CreatedAt: created},
			{Name: "line1\nline2", FileType: "docx", FileSize: 0, Status: "uploaded", CreatedAt: created,
				Tags: []models.Tag{{Name: "work"}, {Name: "tax; 2026"}}},
		}
		rows := parseCSV(t, docsToCSV(docs))
		if len(rows) != 3 {
			t.Fatalf("got %d rows, want header + 2", len(rows))
		}
		if rows[1][0] != `report, "final" version` {
			t.Errorf("comma/quote name mangled: %q", rows[1][0])
		}
		if rows[1][2] != "1234" || rows[1][4] != "2026-08-17T10:30:00Z" {
			t.Errorf("size/created = %q/%q", rows[1][2], rows[1][4])
		}
		if rows[2][0] != "line1\nline2" {
			t.Errorf("embedded newline mangled: %q", rows[2][0])
		}
		// Tag names are joined with "; " — a tag name containing the joiner is
		// ambiguous to split back, which is acceptable for a display column.
		if rows[2][5] != "work; tax; 2026" {
			t.Errorf("tags = %q", rows[2][5])
		}
	})

	t.Run("formula-leading names are not neutralized", func(t *testing.T) {
		// NOTE: pinned gap, not endorsed behavior. A document name beginning
		// with =, +, -, or @ is emitted verbatim, so a spreadsheet that opens
		// the export will evaluate it as a formula (CSV injection). encoding/csv
		// quotes only per RFC4180, which does not defuse formulas. The
		// recommended fix (prefix such cells with ' or a tab) belongs in the
		// hardening register — docs/developer/architecture/backend-hardening.md —
		// and must land as its own change with §8/§9 fan-out, per
		// docs/developer/architecture/testing-strategy.md ("pinned, not
		// silently fixed"). If this test fails because names are now escaped,
		// the fix landed: update the expectations, not the production code.
		for _, prefix := range []string{"=", "+", "-", "@"} {
			name := prefix + `HYPERLINK("http://evil.test","x")`
			out := docsToCSV([]models.Document{{Name: name, CreatedAt: created}})
			rows := parseCSV(t, out)
			if rows[1][0] != name {
				t.Errorf("%s-prefixed name = %q, want verbatim %q (has escaping landed? update this pin)", prefix, rows[1][0], name)
			}
		}
	})
}

// parseCSV reads rendered CSV back through the stdlib parser.
func parseCSV(t *testing.T, data []byte) [][]string {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("rendered CSV does not parse: %v\n%s", err, data)
	}
	return rows
}
