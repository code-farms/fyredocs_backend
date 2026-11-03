// Package sse writes Server-Sent Events frames for ai-service's streamed op
// responses. Headers are committed lazily on the first frame, so a handler
// that fails before producing any output can still answer with a normal JSON
// error envelope and status code.
package sse

import (
	"encoding/json"
	"net/http"
)

// Writer emits `data: <json>\n\n` frames and flushes after each one.
type Writer struct {
	w       http.ResponseWriter
	started bool
}

// New wraps a response writer.
func New(w http.ResponseWriter) *Writer { return &Writer{w: w} }

// Started reports whether any frame (and therefore the 200 status) was sent.
func (s *Writer) Started() bool { return s.started }

// Send writes one event. X-Accel-Buffering disables proxy buffering (nginx,
// Caddy honours Cache-Control) so deltas reach the client as they arrive.
func (s *Writer) Send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if !s.started {
		h := s.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		s.w.WriteHeader(http.StatusOK)
		s.started = true
	}
	if _, err := s.w.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := s.w.Write(data); err != nil {
		return err
	}
	if _, err := s.w.Write([]byte("\n\n")); err != nil {
		return err
	}
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
