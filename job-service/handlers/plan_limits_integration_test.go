//go:build integration

package handlers

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestCreateJob_DB_ExactlyAtJobTotal pins the boundary the clients share:
// a job whose inputs total exactly the plan's per-job MB is accepted, queued
// and committed — the limit is inclusive on both sides of the wire.
func TestCreateJob_DB_ExactlyAtJobTotal(t *testing.T) {
	withDB(t)
	_, client := withMiniRedis(t)
	fs := withFakeStore(t)
	stub := withStubJS(t)

	uid := uuid.New()
	owner := "user:" + uid.String()
	a, b := uuid.NewString(), uuid.NewString()
	for _, id := range []string{a, b} {
		seedUploadObject(t, client, fs, id, "doc.pdf", pdfOfSize(bytesPerMB/2))
		if err := client.HSet(t.Context(), "upload:"+id, "owner", owner).Err(); err != nil {
			t.Fatal(err)
		}
	}
	rec, _ := doJSON(t, jobRouter(seedAuth(uid.String())), http.MethodPost, "/api/optimize-pdf/compress-pdf",
		map[string]any{"uploadIds": []string{a, b}}, map[string]string{
			"X-User-Plan-Max-File-MB": "1", "X-User-Plan-Max-Files": "10", "X-User-Plan-Max-Job-MB": "1",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 at exactly the job total (body %s)", rec.Code, rec.Body.String())
	}
	if n := countRows(t); n != 1 {
		t.Errorf("job rows = %d, want 1", n)
	}
	if len(stub.snapshot()) == 0 {
		t.Error("job at the limit was not dispatched")
	}
}
