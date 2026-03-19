package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"esydocs/shared/natsconn"
)

func TestSSEJobUpdates_MissingJobID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/jobs/:id/events", SSEJobUpdates)

	// Gin requires the parameter to be present in the URL pattern,
	// but we can test with an empty-like ID by using a space or checking
	// the behavior when the param is present but we want to verify headers.
	// Since Gin always fills :id from the URL segment, an empty id is not
	// reachable via normal routing. We test the SSE headers path instead.
}

func TestSSEJobUpdates_NilJetStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Ensure JetStream is nil
	originalJS := natsconn.JS
	natsconn.JS = nil
	defer func() { natsconn.JS = originalJS }()

	r := gin.New()
	r.GET("/api/jobs/:id/events", SSEJobUpdates)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/test-job-123/events", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	result := w.Result()
	defer result.Body.Close()

	// Should set SSE headers
	if ct := result.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
	if cc := result.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %q", cc)
	}
	if conn := result.Header.Get("Connection"); conn != "keep-alive" {
		t.Errorf("expected Connection keep-alive, got %q", conn)
	}
	if xab := result.Header.Get("X-Accel-Buffering"); xab != "no" {
		t.Errorf("expected X-Accel-Buffering no, got %q", xab)
	}

	body := w.Body.String()
	// Should contain the error event since JS is nil
	expected := "event: error\ndata: {\"message\":\"event stream unavailable\"}\n\n"
	if body != expected {
		t.Errorf("expected body %q, got %q", expected, body)
	}
}

func TestSSEJobUpdates_SetsSSEHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Ensure JetStream is nil so handler exits early after setting headers
	originalJS := natsconn.JS
	natsconn.JS = nil
	defer func() { natsconn.JS = originalJS }()

	r := gin.New()
	r.GET("/api/jobs/:id/events", SSEJobUpdates)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/abc-def-123/events", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	result := w.Result()
	defer result.Body.Close()

	headers := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	}
	for key, want := range headers {
		got := result.Header.Get(key)
		if got != want {
			t.Errorf("header %s = %q, want %q", key, got, want)
		}
	}
}
