package procexec

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestCommandNormalRunUnchanged guards the promise that only cancellation
// behaves differently: output and exit status come through as with
// exec.CommandContext.
func TestCommandNormalRunUnchanged(t *testing.T) {
	out, err := Command(context.Background(), "sh", "-c", "echo hi").CombinedOutput()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hi" {
		t.Errorf("output = %q, want %q", out, "hi")
	}

	err = Command(context.Background(), "sh", "-c", "exit 3").Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("err = %v, want exit status 3", err)
	}
}

// TestCommandSetsWaitDelay pins the bound on post-cancel pipe waits.
func TestCommandSetsWaitDelay(t *testing.T) {
	cmd := Command(context.Background(), "true")
	if cmd.WaitDelay != DefaultWaitDelay {
		t.Errorf("WaitDelay = %v, want %v", cmd.WaitDelay, DefaultWaitDelay)
	}
}
