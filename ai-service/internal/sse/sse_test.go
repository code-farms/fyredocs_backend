package sse

import (
	"net/http/httptest"
	"testing"
)

func TestSendCommitsHeadersOnceAndFrames(t *testing.T) {
	rec := httptest.NewRecorder()
	w := New(rec)
	if w.Started() {
		t.Fatal("started before any frame")
	}
	if err := w.Send(map[string]string{"type": "delta", "text": "a"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Send(map[string]string{"type": "done"}); err != nil {
		t.Fatal(err)
	}
	if !w.Started() || rec.Code != 200 {
		t.Fatalf("started=%v code=%d", w.Started(), rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatal("proxy buffering not disabled")
	}
	want := "data: {\"text\":\"a\",\"type\":\"delta\"}\n\ndata: {\"type\":\"done\"}\n\n"
	if rec.Body.String() != want {
		t.Fatalf("body %q", rec.Body.String())
	}
	if !rec.Flushed {
		t.Fatal("frames must be flushed")
	}
}

func TestSendRejectsUnmarshalable(t *testing.T) {
	w := New(httptest.NewRecorder())
	if err := w.Send(make(chan int)); err == nil {
		t.Fatal("expected marshal error")
	}
	if w.Started() {
		t.Fatal("a failed marshal must not commit headers")
	}
}
