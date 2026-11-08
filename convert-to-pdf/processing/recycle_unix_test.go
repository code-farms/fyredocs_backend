//go:build unix

package processing

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// TestRecycleDaemonKillsWholeGroup mirrors production: the daemon leads its own
// process group (setsid in entrypoint.sh), the PID file names the launcher
// (oosplash), and the real worker (soffice.bin) is the launcher's child. Killing
// only the launcher left soffice.bin holding the profile lock; recycleDaemon
// must take down the whole group.
func TestRecycleDaemonKillsWholeGroup(t *testing.T) {
	dir := t.TempDir()
	saved := daemonPIDFile
	daemonPIDFile = func(port string) string { return filepath.Join(dir, "uno-lo-"+port+".pid") }
	t.Cleanup(func() { daemonPIDFile = saved })

	// leader = "unoserver"; it starts a "launcher" that starts a "worker".
	leader := exec.Command("sh", "-c", `sh -c 'sleep 300 & echo $!; wait' & wait`)
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := leader.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := leader.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-leader.Process.Pid, syscall.SIGKILL); _ = leader.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read worker pid: %v", err)
	}
	workerPID, _ := strconv.Atoi(strings.TrimSpace(line))

	// The PID file names a process in the group that is NOT the deepest child,
	// as unoserver's file names oosplash rather than soffice.bin.
	if err := os.WriteFile(daemonPIDFile("2002"), []byte(strconv.Itoa(leader.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}

	recycleDaemon("2002")

	if !processGone(workerPID, 3*time.Second) {
		t.Fatalf("grandchild %d (stand-in for soffice.bin) survived recycleDaemon", workerPID)
	}
}

// TestKillDaemonGroupRefusesOwnGroup guards against a stale PID file whose PID
// was reused inside the worker's own group: that must never kill the worker.
func TestKillDaemonGroupRefusesOwnGroup(t *testing.T) {
	if err := killDaemonGroup(os.Getpid()); !errors.Is(err, errOwnProcessGroup) {
		t.Fatalf("killDaemonGroup(self) = %v, want errOwnProcessGroup", err)
	}
}

// TestKillDaemonGroupGoneIsSuccess: a daemon that already exited is not an error.
func TestKillDaemonGroupGoneIsSuccess(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := killDaemonGroup(cmd.Process.Pid); err != nil {
		t.Errorf("killDaemonGroup(exited pid) = %v, want nil", err)
	}
}
