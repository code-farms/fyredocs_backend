// Tests for the HEALTHCHECK probe binary. Its entire contract is its exit
// code — Docker interprets 0 as healthy and anything else as unhealthy — and
// package main exports nothing, so the tests re-exec the test binary itself
// with HEALTHCHECK_TEST_BE_BINARY set, making TestMain run main() against an
// in-process loopback server. No external binary and no real network are
// involved.
package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain doubles as the probe binary: when HEALTHCHECK_TEST_BE_BINARY is
// set, it rewrites os.Args from HEALTHCHECK_TEST_URL and hands control to
// main(), whose os.Exit codes become the subprocess's exit status.
func TestMain(m *testing.M) {
	if os.Getenv("HEALTHCHECK_TEST_BE_BINARY") == "1" {
		os.Args = []string{"healthcheck"}
		if url := os.Getenv("HEALTHCHECK_TEST_URL"); url != "" {
			os.Args = append(os.Args, url)
		}
		main()
		// main returning (no os.Exit) is the healthy path.
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runProbe re-executes this test binary as the healthcheck probe against url
// (empty means "no argument") and returns its exit code and stderr.
func runProbe(t *testing.T, url string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		"HEALTHCHECK_TEST_BE_BINARY=1",
		"HEALTHCHECK_TEST_URL="+url,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return 0, stderr.String()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stderr.String()
	}
	t.Fatalf("re-exec failed before the probe could run: %v", err)
	return -1, ""
}

func TestProbeExitCodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/nocontent":
			w.WriteHeader(http.StatusNoContent)
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	tests := []struct {
		name         string
		path         string
		wantExit     int
		wantInStderr string
	}{
		{"200 is healthy", "/healthz", 0, ""},
		// Any 2xx is healthy — probes on endpoints answering 204 must not
		// flap the container.
		{"204 is healthy", "/nocontent", 0, ""},
		{"500 is unhealthy", "/broken", 1, "unhealthy: status 500"},
		{"404 is unhealthy", "/missing", 1, "unhealthy: status 404"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, stderr := runProbe(t, server.URL+tt.path)
			if exit != tt.wantExit {
				t.Fatalf("exit = %d, want %d (stderr %q)", exit, tt.wantExit, stderr)
			}
			if tt.wantInStderr != "" && !strings.Contains(stderr, tt.wantInStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tt.wantInStderr)
			}
		})
	}
}

// TestProbeConnectionRefused pins the failure mode HEALTHCHECK exists for: a
// service that is not listening must report unhealthy, not hang or succeed.
func TestProbeConnectionRefused(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	deadURL := server.URL + "/healthz"
	server.Close() // the port is now closed; the URL points at nothing

	exit, stderr := runProbe(t, deadURL)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1 for an unreachable server (stderr %q)", exit, stderr)
	}
	if strings.TrimSpace(stderr) == "" {
		t.Error("connection failure must be reported on stderr for docker inspect")
	}
}

func TestProbeRequiresURLArgument(t *testing.T) {
	exit, stderr := runProbe(t, "")
	if exit != 1 {
		t.Fatalf("exit = %d, want 1 when no URL is given", exit)
	}
	if !strings.Contains(stderr, "usage: healthcheck <url>") {
		t.Errorf("stderr = %q, want the usage line", stderr)
	}
}
