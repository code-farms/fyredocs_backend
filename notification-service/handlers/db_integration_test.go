//go:build integration

// Integration tests for the notification feed's persistence contracts: the
// internal create's sourceId dedupe (and its silent skip on a malformed
// sourceId), per-user feed isolation (IDOR), and the read-state mutations
// scoping their UPDATEs to the caller. The handlers commit through models.DB
// and the dedupe must see rows across requests, so these tests use Truncate
// isolation, never dbtest.Tx (see testing-strategy.md).
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"fyredocs/shared/authverify"
	"fyredocs/shared/database/dbtest"
	"fyredocs/shared/natsconn"

	"notification-service/internal/migrations"
	"notification-service/internal/models"
)

// internalSecret guards the CreateInternal routes in this suite; the token
// matrix itself (fail-closed, wrong secret) is pinned by the unit tests.
const internalSecret = "test-internal-secret"

// withDB applies this service's migrations to the shared test database and
// swaps the package-global handle for the test's duration. The service name
// must stay "notification-service" — it selects the goose history table
// production writes (models.Migrate). It also forces natsconn.Conn to nil so
// createAndPush's best-effort SSE fan-out stays inert: these tests are about
// the durable rows. (A twin helper lives in the subscriber package; promote
// to internal/testutil if a third package ever needs it.)
func withDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := dbtest.Open(t, "notification-service", migrations.FS)
	prev := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = prev })
	prevConn := natsconn.Conn
	natsconn.Conn = nil
	t.Cleanup(func() { natsconn.Conn = prevConn })
	dbtest.Truncate(t, db, "notifications")
}

// feedRouterAll mounts the whole /api feed surface behind RequireUser exactly
// as routes.SetupRouter composes it (the unit suite's feedRouter only mounts
// the pre-DB-guard routes).
func feedRouterAll(uid uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		authverify.SetGinAuth(c, authverify.AuthContext{UserID: uid.String()})
		c.Next()
	})
	n := r.Group("/api/notifications")
	n.Use(RequireUser())
	{
		n.GET("", ListNotifications)
		n.POST("/read-all", MarkAllRead)
		n.POST("/:id/read", MarkRead)
	}
	return r
}

// seedNotif inserts a notification row directly; read=true stamps ReadAt.
func seedNotif(t *testing.T, uid uuid.UUID, title string, read bool) models.Notification {
	t.Helper()
	n := models.Notification{UserID: uid, Type: "info", Title: title}
	if read {
		now := time.Now().UTC()
		n.ReadAt = &now
	}
	if err := models.DB.Create(&n).Error; err != nil {
		t.Fatalf("seed notification %q: %v", title, err)
	}
	return n
}

// notifCount counts rows for one user straight from the table.
func notifCount(t *testing.T, uid uuid.UUID) int64 {
	t.Helper()
	var count int64
	if err := models.DB.Model(&models.Notification{}).Where("user_id = ?", uid).Count(&count).Error; err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	return count
}

// reload fetches a row by id; the read-state tests assert on what the
// database holds, not on what the handler claimed.
func reload(t *testing.T, id uuid.UUID) models.Notification {
	t.Helper()
	var n models.Notification
	if err := models.DB.First(&n, "id = ?", id).Error; err != nil {
		t.Fatalf("reload notification %s: %v", id, err)
	}
	return n
}

// TestCreateInternal_PersistsAndDedupesOnSourceID pins the happy path and the
// exact dedupe semantics for a valid sourceId: the first create writes the
// row (201), the replay answers 200 "already exists" with an empty data
// object and writes nothing — a lost-response retry by a peer service must
// not double-notify the user.
func TestCreateInternal_PersistsAndDedupesOnSourceID(t *testing.T) {
	withDB(t)
	r := internalRouter(internalSecret)
	uid := uuid.Must(uuid.NewV7())
	src := uuid.Must(uuid.NewV7())
	body := `{"userId":"` + uid.String() + `","type":"job.completed","title":"Done","body":"file.pdf","link":"/app/documents","sourceId":"` + src.String() + `"}`

	first := postInternal(r, internalSecret, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create: code = %d (body %s)", first.Code, first.Body.String())
	}
	var row models.Notification
	if err := models.DB.First(&row, "user_id = ?", uid).Error; err != nil {
		t.Fatalf("created row missing: %v", err)
	}
	if row.Type != "job.completed" || row.Title != "Done" || row.Body != "file.pdf" || row.Link != "/app/documents" {
		t.Errorf("row = {type %q title %q body %q link %q}, want the posted fields", row.Type, row.Title, row.Body, row.Link)
	}
	if row.SourceJobID == nil || *row.SourceJobID != src {
		t.Errorf("row.SourceJobID = %v, want %s", row.SourceJobID, src)
	}
	if row.ReadAt != nil {
		t.Error("new notification must start unread")
	}

	replay := postInternal(r, internalSecret, body)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay: code = %d, want 200 dedupe (body %s)", replay.Code, replay.Body.String())
	}
	if got := notifCount(t, uid); got != 1 {
		t.Errorf("rows after replay = %d, want 1 (dedupe must not double-insert)", got)
	}
}

// TestCreateInternal_MalformedSourceIDSkipsDedupe pins current behavior, not
// desired behavior: a sourceId that is not a UUID silently skips BOTH the
// dedupe query and the source_job_id column (row lands with NULL), so the
// partial unique index idx_notif_user_source cannot backstop it and an
// identical replay creates a duplicate notification.
//
// NOTE: security/robustness gap (this week's unit finding #3, see
// backend-hardening.md) — a peer service using non-UUID job identifiers gets
// duplicate notifications on every retry, and the caller receives 201 with no
// hint its sourceId was discarded. Pinned here; the fix (reject or hash
// non-UUID sourceIds) ships separately with its own §8/§9 fan-out.
func TestCreateInternal_MalformedSourceIDSkipsDedupe(t *testing.T) {
	withDB(t)
	r := internalRouter(internalSecret)
	uid := uuid.Must(uuid.NewV7())
	body := `{"userId":"` + uid.String() + `","type":"info","title":"Dup","sourceId":"job-123-not-a-uuid"}`

	for i := 1; i <= 2; i++ {
		if w := postInternal(r, internalSecret, body); w.Code != http.StatusCreated {
			t.Fatalf("post %d: code = %d, want 201 (body %s)", i, w.Code, w.Body.String())
		}
	}
	if got := notifCount(t, uid); got != 2 {
		t.Errorf("rows = %d, want 2 (current behavior: malformed sourceId means no dedupe)", got)
	}
	var rows []models.Notification
	if err := models.DB.Where("user_id = ?", uid).Find(&rows).Error; err != nil {
		t.Fatalf("load rows: %v", err)
	}
	for _, row := range rows {
		if row.SourceJobID != nil {
			t.Errorf("row %s stored SourceJobID %v from a malformed sourceId, want NULL", row.ID, row.SourceJobID)
		}
	}
}

// TestCreateInternal_WhitespaceTypeDefaultsToInfo: "required" binding blocks
// an empty type, so the trim-then-default branch is only reachable with a
// whitespace type — pin that it lands as "info", not as "   ".
func TestCreateInternal_WhitespaceTypeDefaultsToInfo(t *testing.T) {
	withDB(t)
	r := internalRouter(internalSecret)
	uid := uuid.Must(uuid.NewV7())

	w := postInternal(r, internalSecret, `{"userId":"`+uid.String()+`","type":"   ","title":"hi"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d (body %s)", w.Code, w.Body.String())
	}
	var row models.Notification
	if err := models.DB.First(&row, "user_id = ?", uid).Error; err != nil {
		t.Fatalf("row missing: %v", err)
	}
	if row.Type != "info" {
		t.Errorf("row.Type = %q, want info", row.Type)
	}
}

// TestListNotifications_ReturnsOnlyCallersRows is the feed IDOR pin: with two
// users' rows in the table, the list and the unread count must both be scoped
// to the verified caller.
func TestListNotifications_ReturnsOnlyCallersRows(t *testing.T) {
	withDB(t)
	alice := uuid.Must(uuid.NewV7())
	bob := uuid.Must(uuid.NewV7())
	seedNotif(t, alice, "a-unread", false)
	seedNotif(t, alice, "a-read", true)
	seedNotif(t, bob, "b-unread", false)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications", nil)
	w := httptest.NewRecorder()
	feedRouterAll(alice).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d (body %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Notifications []models.Notification `json:"notifications"`
			UnreadCount   int64                 `json:"unreadCount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v\n%s", err, w.Body.String())
	}
	if len(resp.Data.Notifications) != 2 {
		t.Fatalf("returned %d notifications, want alice's 2: %s", len(resp.Data.Notifications), w.Body.String())
	}
	for _, n := range resp.Data.Notifications {
		if n.UserID != alice {
			t.Errorf("feed leaked another user's row: %s belongs to %s", n.ID, n.UserID)
		}
	}
	if resp.Data.UnreadCount != 1 {
		t.Errorf("unreadCount = %d, want 1", resp.Data.UnreadCount)
	}
}

// TestMarkRead_SetsReadFlagOnOwnRow: the one-row read receipt round-trips to
// the database.
func TestMarkRead_SetsReadFlagOnOwnRow(t *testing.T) {
	withDB(t)
	alice := uuid.Must(uuid.NewV7())
	n := seedNotif(t, alice, "unread", false)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/"+n.ID.String()+"/read", nil)
	w := httptest.NewRecorder()
	feedRouterAll(alice).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d (body %s)", w.Code, w.Body.String())
	}
	if got := reload(t, n.ID); got.ReadAt == nil {
		t.Error("read_at still NULL after MarkRead")
	}
}

// TestMarkRead_ForeignRowNotModified pins the IDOR-safe half AND the current
// misleading status: the UPDATE is scoped by user_id, so another user's
// notification is never modified — but the handler does not distinguish
// "matched nothing" from "updated", so the attacker still gets 200.
//
// NOTE: misleading 200 (this week's unit finding #2, see
// backend-hardening.md) — a cross-user MarkRead should surface 404 (rows
// affected == 0), not "Notification marked read". The data is safe (asserted
// below); only the status lies. Pinned here; the fix ships separately.
func TestMarkRead_ForeignRowNotModified(t *testing.T) {
	withDB(t)
	alice := uuid.Must(uuid.NewV7())
	bob := uuid.Must(uuid.NewV7())
	n := seedNotif(t, bob, "bobs-unread", false)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/"+n.ID.String()+"/read", nil)
	w := httptest.NewRecorder()
	feedRouterAll(alice).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d — current behavior is a 200 no-op; a change here means the semantics moved (body %s)", w.Code, w.Body.String())
	}
	if got := reload(t, n.ID); got.ReadAt != nil {
		t.Error("cross-user MarkRead modified another user's row (IDOR)")
	}
}

// TestMarkAllRead_FlipsOnlyCallersRows: the bulk read receipt is scoped by
// user_id — the caller's unread rows flip, everyone else's stay unread.
func TestMarkAllRead_FlipsOnlyCallersRows(t *testing.T) {
	withDB(t)
	alice := uuid.Must(uuid.NewV7())
	bob := uuid.Must(uuid.NewV7())
	a1 := seedNotif(t, alice, "a1", false)
	a2 := seedNotif(t, alice, "a2", false)
	b1 := seedNotif(t, bob, "b1", false)

	req := httptest.NewRequest(http.MethodPost, "/api/notifications/read-all", nil)
	w := httptest.NewRecorder()
	feedRouterAll(alice).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d (body %s)", w.Code, w.Body.String())
	}
	for _, id := range []uuid.UUID{a1.ID, a2.ID} {
		if got := reload(t, id); got.ReadAt == nil {
			t.Errorf("alice's row %s still unread after read-all", id)
		}
	}
	if got := reload(t, b1.ID); got.ReadAt != nil {
		t.Error("read-all flipped another user's row")
	}
}
