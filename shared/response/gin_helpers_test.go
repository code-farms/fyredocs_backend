package response

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func captureSlog(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	prev := slog.Default()
	buf := &bytes.Buffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(h))
	return buf, func() { slog.SetDefault(prev) }
}

func decodeLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		return nil
	}
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("invalid log JSON %q: %v", line, err)
	}
	return m
}

func newGinCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/foo/bar", nil)
	c.Set("requestID", "req-test-1")
	return c, rec
}

func TestErrorfLogsAndRespondsWithEnvelope(t *testing.T) {
	buf, restore := captureSlog(t)
	defer restore()

	c, rec := newGinCtx()
	Errorf(c, http.StatusInternalServerError, "SERVER_ERROR", "Something went wrong.", errors.New("boom"),
		"op", "create_job_dir", "tool", "word-to-pdf")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success || resp.Error == nil || resp.Error.Code != "SERVER_ERROR" {
		t.Errorf("unexpected envelope: %+v", resp)
	}

	line := decodeLogLine(t, buf)
	if line == nil {
		t.Fatal("expected one log line")
	}
	if line["level"] != "ERROR" {
		t.Errorf("expected ERROR level, got %v", line["level"])
	}
	if line["error"] != "boom" {
		t.Errorf("expected error=boom, got %v", line["error"])
	}
	if line["code"] != "SERVER_ERROR" {
		t.Errorf("expected code=SERVER_ERROR, got %v", line["code"])
	}
	if line["op"] != "create_job_dir" {
		t.Errorf("expected op=create_job_dir, got %v", line["op"])
	}
	if line["tool"] != "word-to-pdf" {
		t.Errorf("expected tool=word-to-pdf, got %v", line["tool"])
	}
	// request_id/trace_id enrichment is the shared logger contextHandler's job
	// (covered by shared/logger tests); here we only assert the fields Errorf builds.
	if line["method"] != "POST" {
		t.Errorf("expected method=POST, got %v", line["method"])
	}
	if line["path"] != "/api/foo/bar" {
		t.Errorf("expected path=/api/foo/bar, got %v", line["path"])
	}
}

func TestErrorfNilErrSkipsLog(t *testing.T) {
	buf, restore := captureSlog(t)
	defer restore()

	c, rec := newGinCtx()
	Errorf(c, http.StatusBadRequest, "INVALID_INPUT", "bad", nil)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log output, got %s", buf.String())
	}
}

func TestInternalErrorfReturns500(t *testing.T) {
	buf, restore := captureSlog(t)
	defer restore()

	c, rec := newGinCtx()
	InternalErrorf(c, "SERVER_ERROR", "fail", errors.New("x"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	line := decodeLogLine(t, buf)
	if line == nil {
		t.Fatal("expected one log line")
	}
	if line["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("expected status=500, got %v", line["status"])
	}
}

func TestAbortErrorfAbortsAndLogs(t *testing.T) {
	buf, restore := captureSlog(t)
	defer restore()

	c, rec := newGinCtx()
	AbortErrorf(c, http.StatusUnauthorized, "AUTH_UNAUTHORIZED", "expired", errors.New("token expired"))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if !c.IsAborted() {
		t.Error("expected context aborted")
	}
	line := decodeLogLine(t, buf)
	if line == nil || line["error"] != "token expired" {
		t.Errorf("expected log with error='token expired', got %+v", line)
	}
}
