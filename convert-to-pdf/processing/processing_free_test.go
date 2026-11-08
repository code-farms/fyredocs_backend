package processing

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"
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

// TestTryUnoconvertDeadDaemonFailsFast guards the pre-flight dial: with nothing
// listening on the pooled port, tryUnoconvert must give up immediately instead
// of letting the unoconvert client retry for ~40 s against the 30 s deadline
// (measured 2026-11-08). The port is released back to the pool either way.
func TestTryUnoconvertDeadDaemonFailsFast(t *testing.T) {
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, deadPort, _ := net.SplitHostPort(dead.Addr().String())
	dead.Close()

	saved := pool
	ports := make(chan string, 1)
	ports <- deadPort
	pool = &unoserverPool{host: "127.0.0.1", ports: ports}
	t.Cleanup(func() { pool = saved })

	start := time.Now()
	ok := tryUnoconvert(context.Background(), "in.docx", "out.pdf", "pdf")
	elapsed := time.Since(start)

	if ok {
		t.Fatal("tryUnoconvert reported success against a dead daemon")
	}
	if elapsed > 2*time.Second {
		t.Errorf("tryUnoconvert took %v against a dead daemon, want well under 2s", elapsed)
	}
	if got := len(pool.ports); got != 1 {
		t.Errorf("pool has %d free ports after the call, want 1 (port not released)", got)
	}
}
