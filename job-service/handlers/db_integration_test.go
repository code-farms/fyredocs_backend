//go:build integration

// Integration tests for the DB-backed job lifecycle: CreateJobFromTool's
// committed row + three-layer idempotency scheme over real Postgres rows, the
// authorizeJobAccess ownership matrix through the HTTP surface, history
// scoping/clamping, owner-only deletion, and the cleanup sweep's
// objects-before-rows ordering and consumed-upload fail-safe. These paths
// commit, race goroutines against one handle, and must observe rows across
// connections, so every test uses Truncate isolation — never dbtest.Tx
// (see docs/developer/architecture/testing-strategy.md).
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"fyredocs/shared/authverify"
	"fyredocs/shared/database/dbtest"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"
	"fyredocs/shared/response"
	"fyredocs/shared/storage"

	"job-service/internal/cleanup"
	"job-service/internal/migrations"
	"job-service/internal/models"
)

// withDB applies this service's migrations to the shared test database and
// swaps the package-global handle for the test's duration. The service name
// must stay "job-service" — it selects the goose history table production
// writes (models.Migrate). Truncates every job-service-owned table so a test
// starts clean and leaves nothing behind.
func withDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := dbtest.Open(t, "job-service", migrations.FS)
	prev := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = prev })
	dbtest.Truncate(t, db, "processing_jobs", "file_metadata", "shares")
	return db
}

// publishedMsg records one JetStream publish observed by stubJetStream.
type publishedMsg struct {
	subject string
	data    []byte
}

// stubJetStream satisfies jetstream.JetStream by embedding the interface (nil
// for every method a test never reaches) and overriding PublishMsg to record
// and acknowledge. Needed because CreateJobFromTool's dispatch publish is
// load-bearing — queue.PublishJobEvent has NO nil-handle guard, so job
// creation cannot be exercised end-to-end with natsconn.JS left nil (see
// TestCreateJobFromTool_DB_NilJetStream for the pin of that failure mode).
type stubJetStream struct {
	jetstream.JetStream
	mu        sync.Mutex
	published []publishedMsg
}

func (s *stubJetStream) PublishMsg(_ context.Context, m *nats.Msg, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.published = append(s.published, publishedMsg{subject: m.Subject, data: append([]byte(nil), m.Data...)})
	return &jetstream.PubAck{Stream: "TEST"}, nil
}

// snapshot returns a copy of the recorded publishes, safe to read after
// concurrent handler goroutines have finished.
func (s *stubJetStream) snapshot() []publishedMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]publishedMsg(nil), s.published...)
}

// withStubJS swaps natsconn.JS for a recording stub for the test's duration.
func withStubJS(t *testing.T) *stubJetStream {
	t.Helper()
	stub := &stubJetStream{}
	prev := natsconn.JS
	natsconn.JS = stub
	t.Cleanup(func() { natsconn.JS = prev })
	return stub
}

// seedAuth returns middleware installing a verified AuthContext, mirroring
// what the gin auth middleware does after token verification.
func seedAuth(userID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authverify.SetGinAuth(c, authverify.AuthContext{UserID: userID})
		c.Next()
	}
}

// jobRouter registers the tool-job routes exactly as routes.SetupRouter does
// (minus rate limiters, which fail open in tests anyway). Built by hand
// because importing job-service/routes from this package would be an import
// cycle. All create tests go through the optimize-pdf group; the four
// per-tool-group blocks are identical by design.
func jobRouter(mw ...gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	r.Use(mw...)
	r.POST("/api/optimize-pdf/:tool", CreateJobFromTool)
	r.GET("/api/optimize-pdf/:tool/:id", GetJobByID)
	r.DELETE("/api/optimize-pdf/:tool/:id", DeleteJobByID)
	r.GET("/api/jobs/history", GetJobHistory)
	return r
}

// pdfFixture sniffs as application/pdf (http.DetectContentType matches the
// %PDF- signature), so it passes validateMIMEHead for every pdf-category tool.
var pdfFixture = []byte("%PDF-1.4\n% integration fixture payload for job-service DB tests\n")

// seedOwnedUpload seeds a completed presigned upload bound to the given owner
// identity ("user:<uuid>" / "guest:<token>"), the shape consumeUpload's
// owner check (finding F4) expects. An empty owner seeds a grandfathered
// pre-F4 session with no owner field.
func seedOwnedUpload(t *testing.T, client *redis.Client, fs *fakeStore, uploadID, owner string) string {
	t.Helper()
	key := seedUploadObject(t, client, fs, uploadID, "doc.pdf", pdfFixture)
	if owner != "" {
		if err := client.HSet(context.Background(), "upload:"+uploadID, "owner", owner).Err(); err != nil {
			t.Fatalf("seed upload owner: %v", err)
		}
	}
	return key
}

// postCreateJob submits a JSON job-creation request for compress-pdf.
func postCreateJob(t *testing.T, r *gin.Engine, uploadID string, headers map[string]string) (*httptest.ResponseRecorder, apiEnvelope) {
	t.Helper()
	return doJSON(t, r, http.MethodPost, "/api/optimize-pdf/compress-pdf",
		map[string]any{"uploadId": uploadID}, headers)
}

// countRows returns the number of processing_jobs rows.
func countRows(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := models.DB.Model(&models.ProcessingJob{}).Count(&n).Error; err != nil {
		t.Fatalf("count processing_jobs: %v", err)
	}
	return n
}

// seedJob inserts a job row directly, for tests exercising reads/deletes/sweeps
// rather than the create path.
func seedJob(t *testing.T, userID *uuid.UUID, expiresAt *time.Time) models.ProcessingJob {
	t.Helper()
	job := models.ProcessingJob{
		ID:        uuid.Must(uuid.NewV7()),
		UserID:    userID,
		ToolType:  "compress-pdf",
		Status:    "queued",
		FileName:  "doc.pdf",
		ExpiresAt: expiresAt,
	}
	if err := models.DB.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	return job
}

// seedFileMeta inserts a file_metadata row for a seeded job.
func seedFileMeta(t *testing.T, jobID uuid.UUID, kind, path string) models.FileMetadata {
	t.Helper()
	meta := models.FileMetadata{
		ID:           uuid.Must(uuid.NewV7()),
		JobID:        jobID,
		Kind:         kind,
		OriginalName: "doc.pdf",
		Path:         path,
		SizeBytes:    int64(len(pdfFixture)),
	}
	if err := models.DB.Create(&meta).Error; err != nil {
		t.Fatalf("seed file metadata: %v", err)
	}
	return meta
}

func TestCreateJobFromTool_DB_EndToEnd(t *testing.T) {
	withDB(t)
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	stub := withStubJS(t)

	uid := uuid.New()
	r := jobRouter(seedAuth(uid.String()))
	uploadID := uuid.NewString()
	key := seedOwnedUpload(t, client, fs, uploadID, "user:"+uid.String())

	rec, env := postCreateJob(t, r, uploadID, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if !env.Success || env.Message != "Your file is being processed!" {
		t.Errorf("envelope = success:%v message:%q, want the standard created envelope", env.Success, env.Message)
	}
	jobID, _ := env.Data["id"].(string)
	if jobID == "" {
		t.Fatalf("response has no job id: %s", rec.Body.String())
	}
	if got, _ := env.Data["status"].(string); got != "queued" {
		t.Errorf("response status = %q, want queued", got)
	}
	if got, _ := env.Data["toolType"].(string); got != "compress-pdf" {
		t.Errorf("response toolType = %q, want compress-pdf", got)
	}
	if got, _ := env.Data["outputFileName"].(string); got != "doc.pdf" {
		t.Errorf("response outputFileName = %q, want doc.pdf", got)
	}
	if _, exists := env.Data["guestToken"]; exists {
		t.Error("authenticated create must not mint a guest token")
	}

	// The committed row is the contract, not the in-memory response.
	var job models.ProcessingJob
	if err := models.DB.First(&job, "id = ?", jobID).Error; err != nil {
		t.Fatalf("load committed job row: %v", err)
	}
	if job.Status != "queued" || job.ToolType != "compress-pdf" {
		t.Errorf("row status/tool = %q/%q, want queued/compress-pdf", job.Status, job.ToolType)
	}
	if job.UserID == nil || *job.UserID != uid {
		t.Errorf("row UserID = %v, want %s (identity from verified AuthContext)", job.UserID, uid)
	}
	// Size must come from StatObject, never the client.
	if job.FileSize != int64(len(pdfFixture)) {
		t.Errorf("row FileSize = %d, want %d", job.FileSize, len(pdfFixture))
	}
	if job.ExpiresAt == nil {
		t.Error("row ExpiresAt is nil — every job must get a finite retention expiry")
	}

	var files []models.FileMetadata
	if err := models.DB.Where("job_id = ?", job.ID).Find(&files).Error; err != nil {
		t.Fatalf("load file metadata: %v", err)
	}
	if len(files) != 1 || files[0].Kind != "input" || files[0].Path != key {
		t.Errorf("file_metadata = %+v, want one input row at %q", files, key)
	}

	// Dispatch and analytics both publish; the dispatch payload must carry
	// the committed job's identity and the object key the worker will read.
	pubs := stub.snapshot()
	subjects := map[string]int{}
	for _, p := range pubs {
		subjects[p.subject]++
	}
	if subjects["jobs.dispatch.optimize-pdf"] != 1 || subjects["analytics.events.job.created"] != 1 {
		t.Fatalf("published subjects = %v, want one dispatch and one analytics publish", subjects)
	}
	for _, p := range pubs {
		if p.subject != "jobs.dispatch.optimize-pdf" {
			continue
		}
		var ev queue.JobEvent
		if err := json.Unmarshal(p.data, &ev); err != nil {
			t.Fatalf("decode dispatch event: %v", err)
		}
		if ev.JobID != jobID || len(ev.InputPaths) != 1 || ev.InputPaths[0] != key {
			t.Errorf("dispatch event = %+v, want jobId %s inputPath %s", ev, jobID, key)
		}
	}

	// Post-commit bookkeeping: the upload slot is freed and the
	// consumed-upload mapping points a duplicate POST at this job.
	if exists, _ := client.Exists(context.Background(), "upload:"+uploadID).Result(); exists != 0 {
		t.Error("upload session must be released after the job is queued")
	}
	if got, _ := client.Get(context.Background(), consumedUploadKey(uploadID)).Result(); got != jobID {
		t.Errorf("consumed-upload mapping = %q, want %s", got, jobID)
	}
}

// TestCreateJobFromTool_DB_NilJetStream is the regression guard for the
// nil-JetStream panic: queue.PublishJobEvent once dereferenced a nil handle,
// so a NATS outage panicked AFTER the job row was committed — the recovered
// 500 stranded a row the client could never correlate, and the unreleased
// upload made the retry create a duplicate. The guard turns that into the
// handler's own controlled 500 ("servers are busy"), with the upload session
// preserved for a retry. Deliberately NO recovery middleware here: a
// reintroduced panic fails this test instead of hiding behind a 500.
func TestCreateJobFromTool_DB_NilJetStream(t *testing.T) {
	withDB(t)
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	prev := natsconn.JS
	natsconn.JS = nil
	t.Cleanup(func() { natsconn.JS = prev })

	uid := uuid.New()
	r := jobRouter(seedAuth(uid.String()))
	uploadID := uuid.NewString()
	seedOwnedUpload(t, client, fs, uploadID, "user:"+uid.String())

	rec, env := postCreateJob(t, r, uploadID, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want a controlled 500 (body %s)", rec.Code, rec.Body.String())
	}
	if env.Error == nil || env.Error.Code != response.CodeServerError {
		t.Errorf("error = %+v, want SERVER_ERROR from the handler's own dispatch-failure path", env.Error)
	}
	// The upload session must survive the failure so the client's retry can
	// reuse the same uploadId without re-uploading.
	if exists := client.Exists(context.Background(), "upload:"+uploadID).Val(); exists != 1 {
		t.Error("upload session was released despite the dispatch failure")
	}
}

func TestCreateJobFromTool_DB_IdempotencyKeyDedupe(t *testing.T) {
	withDB(t)
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	withStubJS(t)

	uid := uuid.New()
	r := jobRouter(seedAuth(uid.String()))
	uploadID := uuid.NewString()
	seedOwnedUpload(t, client, fs, uploadID, "user:"+uid.String())
	headers := map[string]string{"Idempotency-Key": "idem-key-1"}

	first, firstEnv := postCreateJob(t, r, uploadID, headers)
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST: status = %d (body %s)", first.Code, first.Body.String())
	}
	// The client retries the SAME request after a lost response: the
	// reservation must resolve to the committed job, not a duplicate.
	second, secondEnv := postCreateJob(t, r, uploadID, headers)
	if second.Code != http.StatusCreated {
		t.Fatalf("replayed POST: status = %d, want 201 with the original job (body %s)",
			second.Code, second.Body.String())
	}
	if firstEnv.Data["id"] != secondEnv.Data["id"] {
		t.Errorf("replay returned job %v, want the original %v", secondEnv.Data["id"], firstEnv.Data["id"])
	}
	if n := countRows(t); n != 1 {
		t.Errorf("processing_jobs rows = %d, want 1 (idempotency must not double-insert)", n)
	}
}

func TestCreateJobFromTool_DB_UploadIDReplayReturnsOriginal(t *testing.T) {
	withDB(t)
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	withStubJS(t)

	uid := uuid.New()
	r := jobRouter(seedAuth(uid.String()))
	uploadID := uuid.NewString()
	seedOwnedUpload(t, client, fs, uploadID, "user:"+uid.String())

	first, firstEnv := postCreateJob(t, r, uploadID, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST: status = %d (body %s)", first.Code, first.Body.String())
	}
	// A DIFFERENT request (no Idempotency-Key) reusing the consumed uploadId:
	// layer 2 (the consumed-upload record) must resolve it to the original
	// job via findExistingJobForUploads instead of "upload not found".
	second, secondEnv := postCreateJob(t, r, uploadID, nil)
	if second.Code != http.StatusCreated {
		t.Fatalf("uploadId replay: status = %d, want 201 with the original job (body %s)",
			second.Code, second.Body.String())
	}
	if firstEnv.Data["id"] != secondEnv.Data["id"] {
		t.Errorf("replay returned job %v, want the original %v", secondEnv.Data["id"], firstEnv.Data["id"])
	}
	if n := countRows(t); n != 1 {
		t.Errorf("processing_jobs rows = %d, want 1 (consumed upload must map to one job)", n)
	}
}

// TestCreateJobFromTool_DB_ConcurrentDuplicate races two identical requests
// (same uploadId, same Idempotency-Key) through the real handler stack. The
// SetNX reservation and upload claims must let exactly one job row commit;
// this needs committed cross-connection visibility, hence Truncate isolation.
func TestCreateJobFromTool_DB_ConcurrentDuplicate(t *testing.T) {
	withDB(t)
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	withStubJS(t)

	uid := uuid.New()
	r := jobRouter(seedAuth(uid.String()))
	uploadID := uuid.NewString()
	seedOwnedUpload(t, client, fs, uploadID, "user:"+uid.String())

	body, err := json.Marshal(map[string]any{"uploadId": uploadID})
	if err != nil {
		t.Fatal(err)
	}

	// No t.Fatal inside the goroutines — results are collected and judged on
	// the main goroutine.
	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range recs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/optimize-pdf/compress-pdf", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "idem-key-conc")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			recs[i] = rec
		}(i)
	}
	wg.Wait()

	if n := countRows(t); n != 1 {
		t.Fatalf("processing_jobs rows = %d, want exactly 1 after concurrent duplicates", n)
	}
	var winner models.ProcessingJob
	if err := models.DB.Take(&winner).Error; err != nil {
		t.Fatalf("load winning job row: %v", err)
	}
	winnerID := winner.ID.String()
	for i, rec := range recs {
		switch rec.Code {
		case http.StatusCreated:
			var env apiEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode response %d: %v", i, err)
			}
			if env.Data["id"] != winnerID {
				t.Errorf("response %d returned job %v, want the single committed row %s", i, env.Data["id"], winnerID)
			}
		case http.StatusConflict:
			// The loser observed the claim/reservation before the winner
			// committed — ALREADY_PROCESSING is the documented answer.
		default:
			t.Errorf("response %d: status = %d, want 201 or 409 (body %s)", i, rec.Code, rec.Body.String())
		}
	}
}

// TestGetJobByID_DB_OwnershipMatrix pins the DB half of authorizeJobAccess
// through the route: unknown and unauthorized are both 404 so job IDs are not
// enumerable.
func TestGetJobByID_DB_OwnershipMatrix(t *testing.T) {
	withDB(t)
	owner := uuid.New()
	job := seedJob(t, &owner, nil)
	path := "/api/optimize-pdf/compress-pdf/" + job.ID.String()

	t.Run("owner gets 200", func(t *testing.T) {
		rec, env := doJSON(t, jobRouter(seedAuth(owner.String())), http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		if env.Data["id"] != job.ID.String() {
			t.Errorf("data.id = %v, want %s", env.Data["id"], job.ID)
		}
	})

	t.Run("other user gets 404", func(t *testing.T) {
		rec, env := doJSON(t, jobRouter(seedAuth(uuid.NewString())), http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 — unauthorized must be indistinguishable from missing", rec.Code)
		}
		if env.Error == nil || env.Error.Code != response.CodeNotFound {
			t.Errorf("error = %+v, want NOT_FOUND", env.Error)
		}
	})

	t.Run("anonymous gets 404", func(t *testing.T) {
		rec, _ := doJSON(t, jobRouter(), http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 for an anonymous caller", rec.Code)
		}
	})
}

// TestJobLifecycle_DB_GuestCookie creates a job anonymously and pins that the
// minted guest token — and only that token — grants access to the row.
func TestJobLifecycle_DB_GuestCookie(t *testing.T) {
	withDB(t)
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	withStubJS(t)

	r := jobRouter() // no auth middleware: anonymous caller
	uploadID := uuid.NewString()
	// Guests have no identity before the create, so the session is seeded
	// ownerless (the grandfathered pre-F4 shape consumeUpload allows).
	seedOwnedUpload(t, client, fs, uploadID, "")

	rec, env := postCreateJob(t, r, uploadID, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("guest create: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	jobID, _ := env.Data["id"].(string)
	guestTok, _ := env.Data["guestToken"].(string)
	if guestTok == "" {
		t.Fatal("guest create must return a guest token")
	}

	var job models.ProcessingJob
	if err := models.DB.First(&job, "id = ?", jobID).Error; err != nil {
		t.Fatalf("load guest job: %v", err)
	}
	if job.UserID != nil {
		t.Errorf("guest job UserID = %v, want nil", job.UserID)
	}

	path := "/api/optimize-pdf/compress-pdf/" + jobID
	t.Run("guest cookie grants access", func(t *testing.T) {
		rec, _ := doJSON(t, r, http.MethodGet, path, nil, map[string]string{
			"Cookie": "guest_token=" + guestTok,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 for the owning guest (body %s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("missing cookie denied", func(t *testing.T) {
		rec, _ := doJSON(t, r, http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 without the guest token", rec.Code)
		}
	})
	t.Run("stranger token denied", func(t *testing.T) {
		rec, _ := doJSON(t, r, http.MethodGet, path, nil, map[string]string{
			"Cookie": "guest_token=" + uuid.NewString(),
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 for a token not holding the job", rec.Code)
		}
	})
}

func TestGetJobHistory_DB_ScopesToCallerAndClampsPagination(t *testing.T) {
	withDB(t)
	userA := uuid.New()
	userB := uuid.New()
	for i := 0; i < 3; i++ {
		seedJob(t, &userA, nil)
	}
	for i := 0; i < 2; i++ {
		seedJob(t, &userB, nil)
	}

	// limit=999 and page=0 are nonsense the handler must clamp, not reject.
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/history?limit=999&page=0", nil)
	rec := httptest.NewRecorder()
	jobRouter(seedAuth(userA.String())).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Data []struct {
			ID     string     `json:"id"`
			UserID *uuid.UUID `json:"userId"`
		} `json:"data"`
		Meta struct {
			Page  int `json:"page"`
			Limit int `json:"limit"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode history: %v\n%s", err, rec.Body.String())
	}
	if len(out.Data) != 3 {
		t.Fatalf("history rows = %d, want only user A's 3", len(out.Data))
	}
	for _, row := range out.Data {
		if row.UserID == nil || *row.UserID != userA {
			t.Errorf("history leaked a row owned by %v", row.UserID)
		}
	}
	if out.Meta.Limit != 100 || out.Meta.Page != 1 {
		t.Errorf("meta = page:%d limit:%d, want clamped to page:1 limit:100", out.Meta.Page, out.Meta.Limit)
	}

	t.Run("anonymous gets 401", func(t *testing.T) {
		rec, env := doJSON(t, jobRouter(), http.MethodGet, "/api/jobs/history", nil, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 — guests have no history", rec.Code)
		}
		if env.Error == nil || env.Error.Code != response.CodeUnauthorized {
			t.Errorf("error = %+v, want AUTH_UNAUTHORIZED", env.Error)
		}
	})
}

func TestDeleteJob_DB_OwnerRemovesObjectsAndRows(t *testing.T) {
	withDB(t)
	withMiniRedis(t)
	fs := withFakeStore(t)

	owner := uuid.New()
	job := seedJob(t, &owner, nil)
	inKey := "uploads/" + job.ID.String() + "/doc.pdf"
	outKey := "jobs/" + job.ID.String() + "/out.pdf"
	seedFileMeta(t, job.ID, "input", inKey)
	seedFileMeta(t, job.ID, "output", outKey)
	fs.putObjectBytes(fs.BucketUploads(), inKey, pdfFixture)
	fs.putObjectBytes(fs.BucketOutputs(), outKey, pdfFixture)

	rec, _ := doJSON(t, jobRouter(seedAuth(owner.String())), http.MethodDelete,
		"/api/optimize-pdf/compress-pdf/"+job.ID.String(), nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}

	if n := countRows(t); n != 0 {
		t.Errorf("processing_jobs rows = %d, want 0 after delete", n)
	}
	var fileCount int64
	if err := models.DB.Model(&models.FileMetadata{}).Where("job_id = ?", job.ID).Count(&fileCount).Error; err != nil {
		t.Fatal(err)
	}
	if fileCount != 0 {
		t.Errorf("file_metadata rows = %d, want 0 after delete", fileCount)
	}
	removed := map[string]bool{}
	for _, r := range fs.removed {
		removed[r] = true
	}
	if !removed[fs.BucketUploads()+"/"+inKey] || !removed[fs.BucketOutputs()+"/"+outKey] {
		t.Errorf("removed objects = %v, want input and output objects removed", fs.removed)
	}
}

func TestDeleteJob_DB_NonOwnerDenied(t *testing.T) {
	withDB(t)
	withMiniRedis(t)
	fs := withFakeStore(t)

	owner := uuid.New()
	job := seedJob(t, &owner, nil)
	inKey := "uploads/" + job.ID.String() + "/doc.pdf"
	seedFileMeta(t, job.ID, "input", inKey)
	fs.putObjectBytes(fs.BucketUploads(), inKey, pdfFixture)

	rec, _ := doJSON(t, jobRouter(seedAuth(uuid.NewString())), http.MethodDelete,
		"/api/optimize-pdf/compress-pdf/"+job.ID.String(), nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (same shape as missing — no existence oracle)", rec.Code)
	}
	if n := countRows(t); n != 1 {
		t.Errorf("processing_jobs rows = %d, want the row intact", n)
	}
	if len(fs.removed) != 0 {
		t.Errorf("objects removed by a denied delete: %v", fs.removed)
	}
}

// sweepStore implements cleanup.ObjectStore, recording removals and running an
// optional onRemove hook AT removal time so tests can observe database state
// mid-sweep (the objects-before-rows ordering is unprovable after the fact).
type sweepStore struct {
	mu       sync.Mutex
	removed  []string
	aborted  []string
	onRemove func(bucket, key string)
}

func (s *sweepStore) BucketUploads() string { return "uploads" }
func (s *sweepStore) BucketOutputs() string { return "outputs" }

func (s *sweepStore) RemoveObject(_ context.Context, bucket, key string) error {
	if s.onRemove != nil {
		s.onRemove(bucket, key)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = append(s.removed, bucket+"/"+key)
	return nil
}

func (s *sweepStore) AbortMultipart(_ context.Context, bucket, key, s3UploadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aborted = append(s.aborted, bucket+"/"+key+"/"+s3UploadID)
	return nil
}

func (s *sweepStore) ListIncompleteUploads(_ context.Context, _ string, _ time.Duration) ([]storage.IncompleteUpload, error) {
	return nil, nil
}

// TestRunSweep_DB_ExpiredJobsObjectsBeforeRows runs the real retention sweep
// against committed rows and pins its two invariants: only expired jobs are
// reaped, and every object removal happens while the rows still exist (a
// crash mid-sweep must leave a retryable row, never an orphaned object).
func TestRunSweep_DB_ExpiredJobsObjectsBeforeRows(t *testing.T) {
	withDB(t)
	withMiniRedis(t) // RunSweep's distributed lock

	owner := uuid.New()
	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)
	expired := seedJob(t, &owner, &past)
	live := seedJob(t, &owner, &future)
	expiredIn := "uploads/" + expired.ID.String() + "/doc.pdf"
	expiredOut := "jobs/" + expired.ID.String() + "/out.pdf"
	liveIn := "uploads/" + live.ID.String() + "/doc.pdf"
	seedFileMeta(t, expired.ID, "input", expiredIn)
	seedFileMeta(t, expired.ID, "output", expiredOut)
	seedFileMeta(t, live.ID, "input", liveIn)

	store := &sweepStore{}
	rowPresentAtRemoval := map[string]bool{}
	store.onRemove = func(bucket, key string) {
		var jobs, files int64
		_ = models.DB.Model(&models.ProcessingJob{}).Where("id = ?", expired.ID).Count(&jobs).Error
		_ = models.DB.Model(&models.FileMetadata{}).Where("job_id = ?", expired.ID).Count(&files).Error
		rowPresentAtRemoval[bucket+"/"+key] = jobs == 1 && files == 2
	}

	cleanup.RunSweep(context.Background(), store)

	wantRemoved := map[string]bool{
		"uploads/" + expiredIn:  true,
		"outputs/" + expiredOut: true,
	}
	if len(store.removed) != len(wantRemoved) {
		t.Fatalf("removed = %v, want exactly the expired job's objects", store.removed)
	}
	for _, key := range store.removed {
		if !wantRemoved[key] {
			t.Errorf("removed unexpected object %q (the live job must be untouched)", key)
		}
		if !rowPresentAtRemoval[key] {
			t.Errorf("object %q was removed AFTER its rows were deleted — ordering violated", key)
		}
	}

	var n int64
	if err := models.DB.Model(&models.ProcessingJob{}).Where("id = ?", expired.ID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("expired job row must be deleted by the sweep")
	}
	if err := models.DB.Model(&models.ProcessingJob{}).Where("id = ?", live.ID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("non-expired job row must survive the sweep")
	}
}

// TestRunSweep_DB_ConsumedUploadKept pins uploadObjectConsumed's positive
// path over real rows: an expired upload session whose object a job consumed
// (referenced by file_metadata.path) is kept for the job sweep; an orphaned
// one is removed.
func TestRunSweep_DB_ConsumedUploadKept(t *testing.T) {
	withDB(t)
	_, client := withMiniRedis(t)

	owner := uuid.New()
	future := time.Now().UTC().Add(time.Hour)
	job := seedJob(t, &owner, &future)
	consumedKey := "uploads/upl-consumed/doc.pdf"
	orphanKey := "uploads/upl-orphan/doc.pdf"
	seedFileMeta(t, job.ID, "input", consumedKey)

	// Both sessions are far past uploadReapAge (2× the 30m default TTL).
	stale := time.Now().UTC().Add(-3 * time.Hour).Format(time.RFC3339)
	for id, key := range map[string]string{"upl-consumed": consumedKey, "upl-orphan": orphanKey} {
		if err := client.HSet(context.Background(), "upload:"+id, map[string]any{
			"createdAt": stale, "key": key,
		}).Err(); err != nil {
			t.Fatalf("seed session %s: %v", id, err)
		}
	}

	store := &sweepStore{}
	cleanup.RunSweep(context.Background(), store)

	if len(store.removed) != 1 || store.removed[0] != "uploads/"+orphanKey {
		t.Errorf("removed = %v, want only the orphaned upload object", store.removed)
	}
	for _, id := range []string{"upl-consumed", "upl-orphan"} {
		if exists, _ := client.Exists(context.Background(), "upload:"+id).Result(); exists != 0 {
			t.Errorf("expired session %s must be deleted from redis", id)
		}
	}
}

// TestRunSweep_DB_ConsumedCheckFailsSafe forces uploadObjectConsumed's query
// to error (a canceled-context session on the same handle) and pins the
// fail-safe: on error it must report "consumed" so the sweep never deletes an
// object it cannot prove is unreferenced.
func TestRunSweep_DB_ConsumedCheckFailsSafe(t *testing.T) {
	db := withDB(t)
	_, client := withMiniRedis(t)

	// Every models.DB query fails with context.Canceled from here on; the
	// underlying handle stays healthy for the Truncate cleanup.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	models.DB = db.Session(&gorm.Session{Context: canceled})

	stale := time.Now().UTC().Add(-3 * time.Hour).Format(time.RFC3339)
	key := "uploads/upl-failsafe/doc.pdf"
	if err := client.HSet(context.Background(), "upload:upl-failsafe", map[string]any{
		"createdAt": stale, "key": key,
	}).Err(); err != nil {
		t.Fatal(err)
	}

	store := &sweepStore{}
	cleanup.RunSweep(context.Background(), store)

	if len(store.removed) != 0 {
		t.Errorf("removed = %v, want none — a failed consumption check must keep the object", store.removed)
	}
	if exists, _ := client.Exists(context.Background(), "upload:upl-failsafe").Result(); exists != 0 {
		t.Error("the expired session itself is still reaped (redis is healthy)")
	}
}

// TestGetJobByID_DB_OutputFileSize pins that a completed job reports its
// output size separately from FileSize (the INPUT total), so the result
// screen can show the compressed size before the user downloads. Unfinished
// jobs omit the field rather than reporting 0 as if the output were empty.
func TestGetJobByID_DB_OutputFileSize(t *testing.T) {
	withDB(t)
	owner := uuid.New()
	router := jobRouter(seedAuth(owner.String()))

	done := seedJob(t, &owner, nil)
	if err := models.DB.Model(&done).Updates(map[string]any{"status": "completed", "file_size": 7_000_000}).Error; err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	out := seedFileMeta(t, done.ID, "output", "outputs/"+done.ID.String()+"/doc.pdf")

	rec, env := doJSON(t, router, http.MethodGet, "/api/optimize-pdf/compress-pdf/"+done.ID.String(), nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := env.Data["outputFileSize"]; got != float64(out.SizeBytes) {
		t.Errorf("outputFileSize = %v, want %d", got, out.SizeBytes)
	}
	if got := env.Data["fileSize"]; got != float64(7_000_000) {
		t.Errorf("fileSize = %v, want the input total 7000000", got)
	}

	queued := seedJob(t, &owner, nil)
	_, env = doJSON(t, router, http.MethodGet, "/api/optimize-pdf/compress-pdf/"+queued.ID.String(), nil, nil)
	if _, present := env.Data["outputFileSize"]; present {
		t.Errorf("queued job reported outputFileSize = %v, want it omitted", env.Data["outputFileSize"])
	}
}
