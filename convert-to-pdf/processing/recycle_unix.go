//go:build unix

package processing

import (
	"errors"
	"os/exec"
	"syscall"
)

// errOwnProcessGroup is returned instead of signalling a process group that the
// worker itself belongs to.
var errOwnProcessGroup = errors.New("refusing to kill the worker's own process group")

// setOwnProcessGroup makes cmd the leader of a new process group, so the
// daemon, its LibreOffice launcher (oosplash) and soffice.bin can be signalled
// as one unit. With Setpgid the group ID equals the child's PID. Must be called
// before Start.
func setOwnProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup sends sig to process group pgid. It refuses the worker's own
// group (a bug elsewhere must never make the worker kill itself) and treats
// ESRCH — the group is already empty — as success.
func signalGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 0 {
		return nil
	}
	if pgid == syscall.Getpgrp() {
		return errOwnProcessGroup
	}
	err := syscall.Kill(-pgid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// termGroup asks a daemon's whole group to exit.
func termGroup(pgid int) error { return signalGroup(pgid, syscall.SIGTERM) }

// killGroup SIGKILLs a daemon's whole group. Used after a daemon exits to
// sweep an orphaned soffice.bin, which would otherwise keep the profile lock
// and UNO port so the restarted daemon could not start.
func killGroup(pgid int) error { return signalGroup(pgid, syscall.SIGKILL) }
