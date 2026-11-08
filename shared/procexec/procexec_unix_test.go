//go:build unix

package procexec

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startWithGrandchild starts `sh` which backgrounds a long sleep (the
// grandchild, standing in for soffice.bin under the libreoffice launcher),
// prints its PID and waits. It returns the grandchild PID once printed.
func startWithGrandchild(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read grandchild pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parse grandchild pid %q: %v", line, err)
	}
	return pid
}

// alive reports whether pid still exists (signal 0 probes without signalling).
// A zombie counts as alive until reaped, so callers poll.
func alive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// gone polls until pid has exited and been reaped, or the deadline passes.
func gone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !alive(pid)
}

const grandchildScript = "sleep 300 & echo $!; wait"

// TestCancelKillsGrandchildren is the property this package exists for: after
// the job context is cancelled, descendants of the command are gone too.
func TestCancelKillsGrandchildren(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := Command(ctx, "sh", "-c", grandchildScript)
	pid := startWithGrandchild(t, cmd)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	cancel()
	_ = cmd.Wait()

	if !gone(pid, 3*time.Second) {
		t.Fatalf("grandchild %d still running after cancel", pid)
	}
}

// TestPlainCommandContextLeaksGrandchild documents why Command is needed: the
// standard library kills only the direct child. If this ever starts failing,
// exec.CommandContext changed and this package may be simplifiable.
func TestPlainCommandContextLeaksGrandchild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "sh", "-c", grandchildScript)
	cmd.WaitDelay = 200 * time.Millisecond
	pid := startWithGrandchild(t, cmd)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	cancel()
	_ = cmd.Wait()

	if gone(pid, 500*time.Millisecond) {
		t.Skip("platform killed the grandchild without a process group; contrast not observable here")
	}
}

// TestCancelDoesNotHangOnInheritedPipes guards the CombinedOutput hang: a
// grandchild holding stdout must not keep the call open after cancellation.
func TestCancelDoesNotHangOnInheritedPipes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Command(ctx, "sh", "-c", "sleep 300 & sleep 300").CombinedOutput()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a cancelled command")
	}
	if elapsed > 3*time.Second {
		t.Errorf("CombinedOutput returned after %v, want well under DefaultWaitDelay", elapsed)
	}
}
