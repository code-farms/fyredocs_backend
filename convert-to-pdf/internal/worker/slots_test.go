package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"fyredocs/shared/jobslots"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// An io job (image-to-pdf) must run while every cpu slot is held by a
// LibreOffice job: the reason workload classes exist (doc 12 §5.1). Before,
// one shared pool made it wait for the office conversion to finish.
func TestIOJobRunsWhileCPUSlotsAreFull(t *testing.T) {
	release := make(chan struct{})
	officeStarted := make(chan struct{})
	imageRan := make(chan struct{})

	cfg := WorkerConfig{
		ServiceName:  "convert-to-pdf",
		AllowedTools: map[string]bool{"word-to-pdf": true, "image-to-pdf": true},
		DB:           dryRunWorkerDB(t),
		Storage:      newFakeStorage(),
		Slots:        jobslots.New("convert-to-pdf", 1, 1),
		Process: func(_ context.Context, _ uuid.UUID, tool string, _ []string, _ map[string]interface{}, outputDir string, _ ProgressFunc) (*ProcessResult, error) {
			switch tool {
			case "word-to-pdf":
				close(officeStarted)
				<-release // hold the only cpu slot
			case "image-to-pdf":
				close(imageRan)
			}
			out := filepath.Join(outputDir, "result.pdf")
			if err := os.WriteFile(out, []byte("%PDF-1.7"), 0o644); err != nil {
				return nil, err
			}
			return &ProcessResult{OutputPath: out}, nil
		},
	}
	msgFor := func(tool, ext string) *jsFakeMsg {
		id := uuid.New()
		b, err := json.Marshal(JobPayload{JobID: id.String(), ToolType: tool,
			InputPaths: []string{"users/u1/" + id.String() + "_in." + ext}})
		if err != nil {
			t.Fatal(err)
		}
		return &jsFakeMsg{data: b, meta: &jetstream.MsgMetadata{NumDelivered: 1}}
	}

	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Add(2)
	go func() {
		defer wg.Done()
		processMessage(context.Background(), cfg, msgFor("word-to-pdf", "docx"), testLogger())
	}()
	select {
	case <-officeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("office job never started")
	}

	go func() {
		defer wg.Done()
		processMessage(context.Background(), cfg, msgFor("image-to-pdf", "png"), testLogger())
	}()
	select {
	case <-imageRan:
		close(release)
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("image-to-pdf (io) waited behind the office job holding the only cpu slot")
	}
}
