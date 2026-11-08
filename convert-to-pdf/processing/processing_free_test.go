package processing

import (
	"context"
	"fmt"
	"testing"
)

// TestFallbackReason pins the closed label set of convert_fallback_total. The
// "no_daemon" case uses the exact text unoconvert prints when nothing listens on
// the pooled port, captured from the shipped image (unoserver 3.7).
func TestFallbackReason(t *testing.T) {
	cases := []struct {
		name   string
		ctxErr error
		output string
		want   string
	}{
		{"deadline", context.DeadlineExceeded, "", "timeout"},
		{"wrapped deadline", fmt.Errorf("unoconvert: %w", context.DeadlineExceeded), "", "timeout"},
		{"deadline wins over refused", context.DeadlineExceeded, "ConnectionRefusedError: [Errno 111] Connection refused", "timeout"},
		{"no daemon", nil, "    sock.connect(sa)\nConnectionRefusedError: [Errno 111] Connection refused", "no_daemon"},
		{"load failure", nil, "Error: source file could not be loaded", "daemon_error"},
		{"cancelled is not timeout", context.Canceled, "", "daemon_error"},
		{"empty", nil, "", "daemon_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fallbackReason(tc.ctxErr, tc.output); got != tc.want {
				t.Errorf("fallbackReason(%v, %q) = %q, want %q", tc.ctxErr, tc.output, got, tc.want)
			}
		})
	}
}
