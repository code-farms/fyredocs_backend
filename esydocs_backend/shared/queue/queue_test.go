package queue

import (
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
