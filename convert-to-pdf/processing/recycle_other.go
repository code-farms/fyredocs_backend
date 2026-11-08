//go:build !unix

package processing

import (
	"errors"
	"os"
	"os/exec"
)

// errNoProcessGroups is returned where process groups do not exist. The worker
// only runs in Linux containers; these stubs exist so the package still builds
// on Windows developer machines, where daemon supervision cannot clean up
// LibreOffice children and is not used.
var errNoProcessGroups = errors.New("process groups are not supported on this platform")

// setOwnProcessGroup is a no-op without process groups.
func setOwnProcessGroup(cmd *exec.Cmd) {}

// termGroup is unsupported without process groups.
func termGroup(pgid int) error { return errNoProcessGroups }

// killGroup kills only the leader without process groups.
func killGroup(pgid int) error {
	proc, err := os.FindProcess(pgid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

// killDaemonGroup kills pid; there is no group to reach its children.
func killDaemonGroup(pid int) error { return killGroup(pid) }
