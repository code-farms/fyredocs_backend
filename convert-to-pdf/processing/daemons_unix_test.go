//go:build unix

package processing

import (
	"context"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHelperFakeUnoserver is not a test: the supervisor tests re-execute the
// test binary into it as a stand-in for unoserver. It listens on FAKE_PORT,
// starts a `sleep` grandchild in its process group (the stand-in for
// soffice.bin) and records that PID in FAKE_DIR, then:
//   - FAKE_EXIT_AFTER=<duration>: exits on its own, leaving the grandchild
//     orphaned in the group — a LibreOffice crash;
//   - FAKE_IGNORE_TERM=1: ignores SIGTERM, as unoserver effectively does while it
//     waits on oosplash;
//   - otherwise runs until killed.
func TestHelperFakeUnoserver(t *testing.T) {
	if os.Getenv("FAKE_UNOSERVER") != "1" {
		t.Skip("helper process for the supervisor tests")
	}
	if os.Getenv("FAKE_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	var ln net.Listener
	var err error
	for i := 0; i < 40; i++ {
		if ln, err = net.Listen("tcp", "127.0.0.1:"+os.Getenv("FAKE_PORT")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		os.Exit(3)
	}
	defer ln.Close()
	gc := exec.Command("sleep", "300")
	if err := gc.Start(); err != nil {
		os.Exit(4)
	}
	name := filepath.Join(os.Getenv("FAKE_DIR"), os.Getenv("FAKE_PORT")+"-"+strconv.Itoa(gc.Process.Pid)+".gc")
	_ = os.WriteFile(name, nil, 0o644)
	if d, err := time.ParseDuration(os.Getenv("FAKE_EXIT_AFTER")); err == nil {
		time.Sleep(d)
		os.Exit(1)
	}
	select {}
}

// freePort returns a TCP port that nothing is listening on right now.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	return p
}

// fakeSupervisor builds a supervisor over n free ports whose daemons are the
// helper process above, configured by extraEnv. It returns the supervisor and
// the directory where each fake records its grandchild PID.
func fakeSupervisor(t *testing.T, n int, extraEnv ...string) (*Supervisor, string) {
	t.Helper()
	dir := t.TempDir()
	s := &Supervisor{host: "127.0.0.1"}
	for i := 0; i < n; i++ {
		s.daemons = append(s.daemons, &daemon{port: freePort(t)})
	}
	s.newCmd = func(d *daemon) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperFakeUnoserver$")
		cmd.Env = append(os.Environ(), "FAKE_UNOSERVER=1", "FAKE_PORT="+d.port, "FAKE_DIR="+dir)
		cmd.Env = append(cmd.Env, extraEnv...)
		return cmd
	}
	s.probe = func(ctx context.Context, port string) bool { return true }
	t.Cleanup(s.Stop)
	return s, dir
}

// grandchildren returns the grandchild PIDs recorded so far for port, oldest
// start first (by file modification time).
func grandchildren(t *testing.T, dir, port string) []int {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, port+"-*.gc"))
	sort.Slice(matches, func(i, j int) bool {
		a, _ := os.Stat(matches[i])
		b, _ := os.Stat(matches[j])
		return a.ModTime().Before(b.ModTime())
	})
	var pids []int
	for _, m := range matches {
		s := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), port+"-"), ".gc")
		if pid, err := strconv.Atoi(s); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// waitFor polls cond until it holds or within elapses.
func waitFor(within time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return cond()
}

// TestSupervisorStartsDaemons: every pooled port gets a running, listening
// daemon and WaitReady reports it.
func TestSupervisorStartsDaemons(t *testing.T) {
	s, _ := fakeSupervisor(t, 2)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if !s.WaitReady(context.Background(), 10*time.Second) {
		t.Fatal("WaitReady = false")
	}
	ports := []string{s.daemons[0].port, s.daemons[1].port}
	if !waitFor(10*time.Second, func() bool { return DaemonsUp("127.0.0.1", ports, 200*time.Millisecond) == 2 }) {
		t.Fatal("not every daemon is listening")
	}
	if got := s.Running(); got != 2 {
		t.Errorf("Running() = %d, want 2", got)
	}
	if activeSupervisor.Load() != s {
		t.Error("Start did not register the supervisor for tryUnoconvert")
	}
}

// TestSupervisorRestartsCrashSweepingOrphan: when a daemon dies on its own and
// leaves its soffice.bin stand-in behind, the supervisor kills that orphan
// before restarting — the orphan is what blocked restarts in the shell version.
func TestSupervisorRestartsCrashSweepingOrphan(t *testing.T) {
	s, dir := fakeSupervisor(t, 1, "FAKE_EXIT_AFTER=300ms")
	port := s.daemons[0].port
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if !waitFor(10*time.Second, func() bool { return len(grandchildren(t, dir, port)) >= 2 }) {
		t.Fatal("crashed daemon was not restarted")
	}
	first := grandchildren(t, dir, port)[0]
	if !processGone(first, 3*time.Second) {
		t.Fatalf("orphaned grandchild %d of the crashed daemon survived the restart", first)
	}
}

// TestSupervisorRecycleRestartsAtOnce: Recycle kills the daemon's whole group
// (including the soffice.bin stand-in) and the replacement starts without
// backoff.
func TestSupervisorRecycleRestartsAtOnce(t *testing.T) {
	s, dir := fakeSupervisor(t, 1)
	port := s.daemons[0].port
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if !waitFor(10*time.Second, func() bool { return len(grandchildren(t, dir, port)) == 1 }) {
		t.Fatal("daemon did not start")
	}
	before := grandchildren(t, dir, port)[0]

	start := time.Now()
	if !recycleViaActiveSupervisor(port) {
		t.Fatal("Recycle reported no running daemon")
	}
	if !processGone(before, 3*time.Second) {
		t.Fatalf("grandchild %d survived Recycle", before)
	}
	if !waitFor(5*time.Second, func() bool { return len(grandchildren(t, dir, port)) == 2 }) {
		t.Fatal("recycled daemon was not restarted")
	}
	// A crash would wait at least 1 s; a recycle must not.
	if elapsed := time.Since(start); elapsed > 900*time.Millisecond {
		t.Errorf("restart after recycle took %v, want no backoff", elapsed)
	}
}

// recycleViaActiveSupervisor goes through the same entry point tryUnoconvert
// uses, proving the active-supervisor registration works.
func recycleViaActiveSupervisor(port string) bool {
	s := activeSupervisor.Load()
	return s != nil && s.Recycle(port)
}

// TestSupervisorStopKillsEverything: Stop leaves no daemon or grandchild alive
// even when the daemon ignores TERM (unoserver blocked on oosplash), and it
// returns within the grace period plus the KILL.
func TestSupervisorStopKillsEverything(t *testing.T) {
	s, dir := fakeSupervisor(t, 2, "FAKE_IGNORE_TERM=1")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	for _, d := range s.daemons {
		d := d
		if !waitFor(10*time.Second, func() bool { return len(grandchildren(t, dir, d.port)) == 1 }) {
			t.Fatalf("daemon on %s did not start", d.port)
		}
	}
	leaders := s.groups()

	start := time.Now()
	s.Stop()
	if elapsed := time.Since(start); elapsed > stopGrace+2*time.Second {
		t.Errorf("Stop took %v, want about stopGrace (%v)", elapsed, stopGrace)
	}
	for _, d := range s.daemons {
		for _, pid := range grandchildren(t, dir, d.port) {
			if !processGone(pid, 2*time.Second) {
				t.Errorf("grandchild %d of %s survived Stop", pid, d.port)
			}
		}
	}
	for _, pid := range leaders {
		if !processGone(pid, 2*time.Second) {
			t.Errorf("daemon %d survived Stop", pid)
		}
	}
	if got := s.Running(); got != 0 {
		t.Errorf("Running() after Stop = %d, want 0", got)
	}
	if activeSupervisor.Load() == s {
		t.Error("Stop left the supervisor registered")
	}
}
