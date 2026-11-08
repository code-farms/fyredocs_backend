//go:build unix

package procexec

import (
	"errors"
	"os/exec"
	"syscall"
)

// setProcessGroup makes cmd the leader of a new process group and replaces the
// default cancel (kill the child only) with a kill of the whole group. With
// Setpgid the group ID equals the child's PID, so -Pid addresses the group.
// ESRCH (group already gone) is not an error: the work we wanted stopped has
// stopped. Must be called before Start.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
}
