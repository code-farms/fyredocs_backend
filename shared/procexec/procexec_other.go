//go:build !unix

package procexec

import "os/exec"

// setProcessGroup is a no-op where Unix process groups do not exist (Windows
// developer machines — optimize-pdf still looks up gswin64c there). Cancellation
// then falls back to exec.CommandContext's default of killing the direct child;
// WaitDelay still bounds the wait. Production images are Linux and take the
// group-killing path in procexec_unix.go.
func setProcessGroup(cmd *exec.Cmd) {}
