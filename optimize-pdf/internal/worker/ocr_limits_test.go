package worker

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPlanOCRJob(t *testing.T) {
	for _, k := range []string{"OCR_SECONDS_PER_PAGE", "OCR_JOB_BASE_SECONDS", "OCR_MAX_JOB_MINUTES"} {
		t.Setenv(k, "") // defaults: 40 s/page, 60 s base, 25 min cap
	}
	cases := []struct {
		name              string
		pages, limit, par int
		wantDeadline      time.Duration
		wantErr           bool
		wantInMsg         string
	}{
		// 5 pages: 60 + 5×40 = 260 s → ×1.5 = 6.5 min → floor 10 min.
		{"small scan keeps 10 min", 5, 30, 1, 10 * time.Minute, false, ""},
		// 30 pages on one slot: 60 + 1200 = 21 min → ×1.5 capped at 25 min.
		{"free limit fits one slot", 30, 30, 1, 25 * time.Minute, false, ""},
		{"over plan limit", 31, 30, 1, 0, true, "your plan allows up to 30 pages"},
		{"guest limit", 12, 10, 4, 0, true, "allows up to 10 pages"},
		// 200 pages on one slot: 60 + 8000 s ≫ 25 min; fits (1500-60)/40 = 36.
		{"too long for one slot", 200, 200, 1, 0, true, "parts of up to 36 pages"},
		// 200 pages on 6 slots: 34 rounds → 60 + 1360 = 1420 s ≤ 1500 s.
		{"pro on six slots fits", 200, 200, 6, 25 * time.Minute, false, ""},
		// No limit on the dispatch (older job-service): only the time check.
		{"no plan limit", 20, 0, 1, 21*time.Minute + 30*time.Second, false, ""}, // 60 + 800 = 860 s × 1.5
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := planOCRJob(tc.pages, tc.limit, tc.par)
			if tc.wantErr {
				var ple *pageLimitError
				if !errors.As(err, &ple) {
					t.Fatalf("err = %v, want *pageLimitError", err)
				}
				if !strings.Contains(ple.userMessage, tc.wantInMsg) {
					t.Errorf("message %q does not contain %q", ple.userMessage, tc.wantInMsg)
				}
				if classifyError(err) != ErrCodeTooManyPages {
					t.Errorf("classifyError = %s, want %s", classifyError(err), ErrCodeTooManyPages)
				}
				if isRecoverable(err) {
					t.Error("a page-limit rejection must not be retried")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d != tc.wantDeadline {
				t.Errorf("deadline = %v, want %v", d, tc.wantDeadline)
			}
		})
	}
}

func TestPlanOCRJobCapStaysBelowAckWait(t *testing.T) {
	t.Setenv("OCR_MAX_JOB_MINUTES", "90")
	d, err := planOCRJob(1, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if d >= ConsumerAckWait {
		t.Errorf("deadline %v reaches the 30-minute AckWait", d)
	}
	if got := ocrMaxJobMinutes(); got != OCRMaxJobMinutesCeiling {
		t.Errorf("ocrMaxJobMinutes = %d, want clamp %d", got, OCRMaxJobMinutesCeiling)
	}
}
