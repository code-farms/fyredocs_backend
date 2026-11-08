package processing

import (
	"testing"
	"time"
)

// TestRestartDelay pins the backoff rules carried over from the shell
// supervisor: a deliberate recycle restarts at once, crashes back off by a
// second each up to 30 s, and a minute of uptime resets the count.
func TestRestartDelay(t *testing.T) {
	cases := []struct {
		name         string
		failures     int
		uptime       time.Duration
		recycled     bool
		wantDelay    time.Duration
		wantFailures int
	}{
		{"recycle restarts at once", 5, time.Second, true, 0, 0},
		{"first crash", 0, time.Second, false, 1 * time.Second, 1},
		{"third quick crash", 2, time.Second, false, 3 * time.Second, 3},
		{"capped at 30s", 40, time.Second, false, 30 * time.Second, 41},
		{"stable uptime resets", 40, 61 * time.Second, false, 1 * time.Second, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			delay, failures := restartDelay(tc.failures, tc.uptime, tc.recycled)
			if delay != tc.wantDelay || failures != tc.wantFailures {
				t.Errorf("restartDelay(%d, %v, %v) = (%v, %d), want (%v, %d)",
					tc.failures, tc.uptime, tc.recycled, delay, failures, tc.wantDelay, tc.wantFailures)
			}
		})
	}
}

// TestGoSupervisionEnabled pins the rollback switch shared with entrypoint.sh.
func TestGoSupervisionEnabled(t *testing.T) {
	for value, want := range map[string]bool{"": true, "go": true, "shell": false, "anything": true} {
		t.Setenv(supervisorEnv, value)
		if got := GoSupervisionEnabled(); got != want {
			t.Errorf("%s=%q: GoSupervisionEnabled() = %v, want %v", supervisorEnv, value, got, want)
		}
	}
}

// TestNewSupervisorPortsAndOffset guards the single source of truth: the
// supervisor launches exactly the ports the Go pool hands out, each with a
// distinct UNO port (D23b) and its own profile path (D23).
func TestNewSupervisorPortsAndOffset(t *testing.T) {
	t.Setenv("UNOSERVER_PORT", "2002")
	t.Setenv("UNOSERVER_INSTANCES", "3")
	t.Setenv("UNOSERVER_UNO_PORT_OFFSET", "1") // smaller than the pool: must be corrected
	s := NewSupervisor()
	if len(s.daemons) != 3 {
		t.Fatalf("daemons = %d, want 3", len(s.daemons))
	}
	seen := map[string]bool{}
	for i, d := range s.daemons {
		if want := UnoserverPorts()[i]; d.port != want {
			t.Errorf("daemon %d port = %s, want %s", i, d.port, want)
		}
		for _, p := range []string{d.port, d.unoPort} {
			if seen[p] {
				t.Errorf("port %s used twice", p)
			}
			seen[p] = true
		}
	}
	if s.daemons[0].unoPort != "2102" {
		t.Errorf("uno port = %s, want 2102 (offset reset to 100)", s.daemons[0].unoPort)
	}
	args := s.newCmd(s.daemons[0]).Args
	for i, a := range args {
		if a == "--user-installation" && (i+1 >= len(args) || len(args[i+1]) == 0 || args[i+1][0] != '/') {
			t.Errorf("--user-installation must be an absolute path, got %v", args)
		}
	}
}

// TestSupervisorRecycleAndStopWithoutStart: both are safe on a supervisor that
// never started (the shell fallback, or unoserver missing).
func TestSupervisorRecycleAndStopWithoutStart(t *testing.T) {
	t.Setenv("UNOSERVER_INSTANCES", "1")
	s := NewSupervisor()
	if s.Recycle("2002") {
		t.Error("Recycle on a stopped supervisor reported success")
	}
	if s.Recycle("9999") {
		t.Error("Recycle of an unknown port reported success")
	}
	s.Stop()
	s.Stop()
}
