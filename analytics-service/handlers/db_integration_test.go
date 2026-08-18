//go:build integration

// Integration tests for the activity persistence contract: the
// client_event_id ON CONFLICT dedupe (the response's accepted/deduplicated
// split must reflect what actually hit the table), sensitive-metadata
// scrubbing surviving the round-trip to jsonb, and the admin trend queries
// executing real SQL. These need committed, cross-connection-visible rows, so
// they use Truncate isolation, never dbtest.Tx (see testing-strategy.md).
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"fyredocs/shared/authverify"

	"fyredocs/shared/database/dbtest"

	"analytics-service/internal/migrations"
	"analytics-service/internal/models"
)

// withDB applies this service's migrations to the shared test database and
// swaps the package-global handle for the test's duration. The service name
// must stay "analytics-service" — it selects the goose history table
// production writes (models.Migrate).
func withDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := dbtest.Open(t, "analytics-service", migrations.FS)
	prev := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = prev })
	dbtest.Truncate(t, db, "user_activity")
}

// seedAuth returns middleware installing a verified AuthContext, mirroring
// what the gin auth middleware does after token verification.
func seedAuth(userID string, isGuest bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		authverify.SetGinAuth(c, authverify.AuthContext{UserID: userID, IsGuest: isGuest})
		c.Next()
	}
}

func doGet(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

type ingestResult struct {
	Data struct {
		Accepted     int64 `json:"accepted"`
		Deduplicated int64 `json:"deduplicated"`
		Rejected     []struct {
			Index  int    `json:"index"`
			Reason string `json:"reason"`
		} `json:"rejected"`
	} `json:"data"`
}

func decodeIngest(t *testing.T, body []byte) ingestResult {
	t.Helper()
	var out ingestResult
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode ingest response: %v\n%s", err, body)
	}
	return out
}

func TestIngestActivity_PersistsAndDedupes(t *testing.T) {
	withDB(t)
	r := ingestRouter(nil)

	first := postEvents(t, r, map[string]any{"events": []any{
		validEvent("evt-1"), validEvent("evt-2"),
	}})
	if first.Code != http.StatusOK {
		t.Fatalf("first batch: code = %d, body = %s", first.Code, first.Body.String())
	}
	got := decodeIngest(t, first.Body.Bytes())
	if got.Data.Accepted != 2 || got.Data.Deduplicated != 0 {
		t.Fatalf("first batch: accepted/deduplicated = %d/%d, want 2/0",
			got.Data.Accepted, got.Data.Deduplicated)
	}

	// A client retries its whole outbox after a lost 200: every row must
	// dedupe on client_event_id and the response must say so honestly.
	replay := postEvents(t, r, map[string]any{"events": []any{
		validEvent("evt-1"), validEvent("evt-2"), validEvent("evt-3"),
	}})
	got = decodeIngest(t, replay.Body.Bytes())
	if got.Data.Accepted != 1 || got.Data.Deduplicated != 2 {
		t.Fatalf("replay: accepted/deduplicated = %d/%d, want 1/2",
			got.Data.Accepted, got.Data.Deduplicated)
	}

	var count int64
	if err := models.DB.Model(&models.UserActivity{}).Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 3 {
		t.Fatalf("user_activity rows = %d, want 3 (dedupe must not double-insert)", count)
	}
}

func TestIngestActivity_ScrubbedMetadataRoundTrip(t *testing.T) {
	withDB(t)
	r := ingestRouter(nil)

	ev := validEvent("evt-meta")
	ev["metadata"] = map[string]any{
		"password":   "hunter2",
		"authToken":  "abc",
		"pageCount":  3,
		"toolResult": "ok",
	}
	w := postEvents(t, r, map[string]any{"events": []any{ev}})
	if got := decodeIngest(t, w.Body.Bytes()); got.Data.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1 (body %s)", got.Data.Accepted, w.Body.String())
	}

	var row models.UserActivity
	if err := models.DB.Where("client_event_id = ?", "evt-meta").First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	stored := string(row.Metadata)
	// What matters is what the DATABASE holds, not what sanitizeMetadata
	// returned in memory — a marshal/jsonb quirk could resurrect a key.
	for _, leaked := range []string{"password", "hunter2", "authToken"} {
		if strings.Contains(stored, leaked) {
			t.Errorf("stored metadata leaks %q: %s", leaked, stored)
		}
	}
	for _, kept := range []string{"pageCount", "toolResult"} {
		if !strings.Contains(stored, kept) {
			t.Errorf("stored metadata lost benign key %q: %s", kept, stored)
		}
	}
}

func TestIngestActivity_IdentityFromAuthContextNotBody(t *testing.T) {
	withDB(t)

	// The event body has no user field at all, so the only way a row can
	// carry this UUID is the verified AuthContext — pinning that clients
	// cannot attribute activity to someone else.
	const uid = "0198c5d0-0000-7000-8000-00000000aaaa"
	r := ingestRouter(seedAuth(uid, false))

	w := postEvents(t, r, map[string]any{"events": []any{validEvent("evt-owned")}})
	if got := decodeIngest(t, w.Body.Bytes()); got.Data.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1 (body %s)", got.Data.Accepted, w.Body.String())
	}

	var row models.UserActivity
	if err := models.DB.Where("client_event_id = ?", "evt-owned").First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.UserID == nil || row.UserID.String() != uid {
		t.Errorf("row.UserID = %v, want %s", row.UserID, uid)
	}
	if row.IsGuest {
		t.Error("authenticated ingest stored a guest row")
	}
}

func TestAPITrends_WithDB_ExecutesAndKeepsShape(t *testing.T) {
	// The unit twin of this test skips itself when a DB is present; this one
	// runs the real SQL against empty tables — the shape contract must hold
	// on both paths.
	withDB(t)
	r := gin.New()
	r.GET("/admin/metrics/api-trends", APITrends)

	w := doGet(t, r, "/admin/metrics/api-trends?days=7")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Series       []any `json:"series"`
			ErrorClasses []any `json:"errorClasses"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	if !body.Success {
		t.Fatalf("success = false: %s", w.Body.String())
	}
	if body.Data.Series == nil || body.Data.ErrorClasses == nil {
		t.Errorf("series/errorClasses must be empty arrays, not null: %s", w.Body.String())
	}
}
