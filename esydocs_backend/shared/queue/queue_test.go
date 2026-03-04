package queue

import (
	"encoding/json"
	"testing"
)

func TestSubjectForDispatch(t *testing.T) {
	got := SubjectForDispatch("convert-from-pdf")
	want := "jobs.dispatch.convert-from-pdf"
	if got != want {
		t.Errorf("SubjectForDispatch = %q, want %q", got, want)
	}
}

func TestSubjectForEvent(t *testing.T) {
	got := SubjectForEvent("completed")
	want := "jobs.events.completed"
	if got != want {
		t.Errorf("SubjectForEvent = %q, want %q", got, want)
	}
}

func TestJobEventMarshal(t *testing.T) {
	event := JobEvent{
		EventType: "JobCreated",
		JobID:     "test-id",
		ToolType:  "word-to-pdf",
		Attempts:  1,
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var decoded JobEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.EventType != "JobCreated" {
		t.Errorf("expected EventType 'JobCreated', got %q", decoded.EventType)
	}
	if decoded.JobID != "test-id" {
		t.Errorf("expected JobID 'test-id', got %q", decoded.JobID)
	}
}

func TestSubjectForDispatchVariousServices(t *testing.T) {
	tests := []struct {
		service string
		want    string
	}{
		{"convert-to-pdf", "jobs.dispatch.convert-to-pdf"},
		{"convert-from-pdf", "jobs.dispatch.convert-from-pdf"},
		{"organize-pdf", "jobs.dispatch.organize-pdf"},
		{"optimize-pdf", "jobs.dispatch.optimize-pdf"},
	}
	for _, tt := range tests {
		got := SubjectForDispatch(tt.service)
		if got != tt.want {
			t.Errorf("SubjectForDispatch(%q) = %q, want %q", tt.service, got, tt.want)
		}
	}
}
