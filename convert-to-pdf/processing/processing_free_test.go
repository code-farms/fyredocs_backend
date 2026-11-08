package processing

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

// TestUnoconvertTimeout pins the size-scaled deadline (2 s + 1.5 s/MB, floor
// 30 s). The 20 MB and 100 MB rows are the cases the old fixed 30 s deadline
// failed on every time, sending large files to a cold LibreOffice.
func TestUnoconvertTimeout(t *testing.T) {
	const mb = 1 << 20
	cases := []struct {
		bytes int64
		want  time.Duration
	}{
		{-1, 30 * time.Second},
		{0, 30 * time.Second},
		{1 * mb, 30 * time.Second},
		{18 * mb, 30 * time.Second},
		{20 * mb, 32 * time.Second},
		{100 * mb, 152 * time.Second},
		{500 * mb, 752 * time.Second},
	}
	for _, tc := range cases {
		if got := unoconvertTimeout(tc.bytes); got != tc.want {
			t.Errorf("unoconvertTimeout(%d) = %v, want %v", tc.bytes, got, tc.want)
		}
	}
}

// TestRecycleDaemonToleratesMissingOrBadPIDFile: recycling is best-effort and
// must never panic or kill something arbitrary when the file is absent or junk.
func TestRecycleDaemonToleratesMissingOrBadPIDFile(t *testing.T) {
	dir := t.TempDir()
	saved := daemonPIDFile
	daemonPIDFile = func(port string) string { return filepath.Join(dir, "uno-lo-"+port+".pid") }
	t.Cleanup(func() { daemonPIDFile = saved })

	recycleDaemon("2002") // missing file
	for _, junk := range []string{"", "abc", "0", "-5"} {
		if err := os.WriteFile(daemonPIDFile("2003"), []byte(junk), 0o644); err != nil {
			t.Fatal(err)
		}
		recycleDaemon("2003")
	}
}
