//go:build unix

package processing

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// processGone polls until pid no longer exists (or is a reaped-pending zombie
// whose group was killed), up to within.
func processGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestKillGroupRefusesOwnGroup guards against a bug elsewhere making the
// worker signal its own process group: that must never kill the worker.
func TestKillGroupRefusesOwnGroup(t *testing.T) {
	if err := killGroup(syscall.Getpgrp()); !errors.Is(err, errOwnProcessGroup) {
		t.Fatalf("killGroup(own group) = %v, want errOwnProcessGroup", err)
	}
}

// TestKillGroupEmptyIsSuccess: signalling a group whose processes have all
// exited is not an error (the daemon already went away on its own).
func TestKillGroupEmptyIsSuccess(t *testing.T) {
	cmd := exec.Command("true")
	setOwnProcessGroup(cmd)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := killGroup(cmd.Process.Pid); err != nil {
		t.Errorf("killGroup(exited group) = %v, want nil", err)
	}
}
