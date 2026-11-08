package processing

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"fyredocs/shared/metrics"
	"fyredocs/shared/procexec"
)

// Daemon supervision. The worker starts, restarts, recycles and stops the
// unoserver (LibreOffice) daemons itself; there is no shell entrypoint. This
// replaced a shell supervisor that computed the port list a second time (with a
// different default), needed a PID-file contract to recycle a daemon, and hid
// every lifecycle bug from the Go tests (docs/developer/architecture/
// next-generation-document-engine/20-implementation-roadmap.md, M1.4).
//
// Each daemon runs as the leader of its own process group, which holds
// unoserver, LibreOffice's oosplash launcher and soffice.bin. The group is the
// unit every operation acts on: signalling unoserver alone leaves oosplash or
// an orphaned soffice.bin behind, and that orphan keeps the profile lock and UNO
// port so the next daemon on the port cannot start (measured 2026-11-08).

const (
	// supervisorService is the metrics "service" label.
	supervisorService = "convert-to-pdf"
	// minStableUptime: a daemon that ran this long before exiting resets the
	// crash backoff, so a one-off crash restarts almost immediately.
	minStableUptime = 60 * time.Second
	// maxRestartDelay caps the crash backoff so a misconfiguration does not spin
	// the CPU but a fixed problem recovers within half a minute.
	maxRestartDelay = 30 * time.Second
	// stopGrace is how long Stop waits after TERM before KILL. Daemons are only
	// stopped after the worker has drained, so no conversion is in flight;
	// soffice.bin exits within about a second of TERM, oosplash never does.
	stopGrace = 2 * time.Second
	// readyPollInterval is how often WaitReady probes the first daemon.
	readyPollInterval = 500 * time.Millisecond
)

// activeSupervisor is the running Supervisor, read by tryUnoconvert to recycle a
// daemon. nil means no daemons are supervised (unoserver missing, e.g. a local
// `go run`, or the supervisor has stopped); recycling is then a no-op.
var activeSupervisor atomic.Pointer[Supervisor]

// daemon is one supervised unoserver. All mutable fields are guarded by mu.
type daemon struct {
	port    string
	unoPort string
	profile string

	mu sync.Mutex
	// pgid of the running process group; 0 while not running.
	pgid int
	// recycled is set by Recycle so the loop restarts without backoff and labels
	// the restart "recycle" rather than "crash".
	recycled bool
}

// Supervisor keeps one unoserver daemon running per pooled port.
type Supervisor struct {
	host    string
	daemons []*daemon
	// newCmd builds the daemon command; replaced in tests by a fake daemon.
	newCmd func(d *daemon) *exec.Cmd
	// probe reports whether the daemon on port serves requests; replaced in tests.
	probe func(ctx context.Context, port string) bool

	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopping atomic.Bool
	running  atomic.Int64
}

// NewSupervisor builds a supervisor for every port in UnoserverPorts(), reading
// UNOSERVER_UNO_PORT_OFFSET (default 100) and UNOSERVER_CONVERSION_TIMEOUT
// (seconds, default 600).
func NewSupervisor() *Supervisor {
	host := UnoserverHost()
	ports := UnoserverPorts()

	offset := envInt("UNOSERVER_UNO_PORT_OFFSET", 100)
	if offset < len(ports) {
		// A smaller offset would put a UNO port on another daemon's XML-RPC port.
		slog.Warn("UNOSERVER_UNO_PORT_OFFSET smaller than the pool; using 100",
			"offset", offset, "instances", len(ports))
		offset = 100
	}
	convTimeout := envInt("UNOSERVER_CONVERSION_TIMEOUT", 600)

	s := &Supervisor{host: host}
	for _, p := range ports {
		n, _ := strconv.Atoi(p)
		s.daemons = append(s.daemons, &daemon{
			port:    p,
			unoPort: strconv.Itoa(n + offset),
			profile: filepath.Join(os.TempDir(), "uno-profile-"+p),
		})
	}
	s.newCmd = func(d *daemon) *exec.Cmd {
		// --user-installation takes a PATH (unoserver converts it to a URI);
		// --uno-port must differ from --port. Both were D23/D23b.
		return exec.Command("unoserver",
			"--interface", host,
			"--port", d.port,
			"--uno-port", d.unoPort,
			"--user-installation", d.profile,
			"--conversion-timeout", strconv.Itoa(convTimeout))
	}
	s.probe = func(ctx context.Context, port string) bool {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return procexec.Command(pctx, "unoping", "--host", host, "--port", port).Run() == nil
	}
	return s
}

// envInt reads a positive integer environment variable, or returns def.
func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return def
}

// Start launches every daemon under its own restart loop and makes the
// supervisor the one tryUnoconvert recycles through. It returns
// exec.ErrNotFound-style errors when unoserver is not installed (local `go run`),
// in which case nothing is started and conversions use the cold path. The loops
// run until Stop; they deliberately do not follow the worker's context, which
// is cancelled at the START of shutdown while daemons must outlive the drain.
func (s *Supervisor) Start() error {
	if _, err := exec.LookPath(s.newCmd(&daemon{}).Path); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for _, d := range s.daemons {
		s.wg.Add(1)
		go s.loop(ctx, d)
	}
	activeSupervisor.Store(s)
	return nil
}

// loop keeps d running until the supervisor stops. After every exit it sweeps
// the daemon's process group (an orphaned soffice.bin would block the restart)
// and restarts after restartDelay.
func (s *Supervisor) loop(ctx context.Context, d *daemon) {
	defer s.wg.Done()
	failures := 0
	for {
		if s.stopping.Load() {
			return
		}
		cmd := s.newCmd(d)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		setOwnProcessGroup(cmd)

		slog.Info("starting unoserver daemon", "port", d.port, "unoPort", d.unoPort, "profile", d.profile)
		started := time.Now()
		var recycled bool
		if err := cmd.Start(); err != nil {
			slog.Error("unoserver daemon failed to start", "port", d.port, "error", err)
		} else {
			d.mu.Lock()
			d.pgid = cmd.Process.Pid
			d.recycled = false
			d.mu.Unlock()
			s.setRunning(+1)

			err := cmd.Wait()

			d.mu.Lock()
			pgid := d.pgid
			recycled = d.recycled
			d.pgid = 0
			d.mu.Unlock()
			s.setRunning(-1)
			if err := killGroup(pgid); err != nil {
				slog.Warn("sweeping unoserver daemon group failed", "port", d.port, "pgid", pgid, "error", err)
			}
			if s.stopping.Load() {
				return
			}
			reason := "crash"
			if recycled {
				reason = "recycle"
			}
			metrics.EngineDaemonRestarts.WithLabelValues(supervisorService, reason).Inc()
			slog.Warn("unoserver daemon exited", "port", d.port, "reason", reason, "error", err,
				"uptime", time.Since(started).Round(time.Second))
		}

		var delay time.Duration
		delay, failures = restartDelay(failures, time.Since(started), recycled)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// restartDelay returns how long to wait before restarting a daemon and the new
// consecutive-failure count. A deliberate recycle restarts at once and resets
// the count, because counting recycles as failures would back a busy daemon off
// towards 30 s and push jobs onto the cold path. A crash waits one second per
// consecutive quick crash, capped at maxRestartDelay; a daemon that stayed up
// for minStableUptime starts the count again.
func restartDelay(failures int, uptime time.Duration, recycled bool) (time.Duration, int) {
	if recycled {
		return 0, 0
	}
	if uptime >= minStableUptime {
		failures = 0
	}
	failures++
	delay := time.Duration(failures) * time.Second
	if delay > maxRestartDelay {
		delay = maxRestartDelay
	}
	return delay, failures
}

// setRunning adjusts the running count and its gauge.
func (s *Supervisor) setRunning(delta int64) {
	n := s.running.Add(delta)
	metrics.EngineDaemonsRunning.WithLabelValues(supervisorService).Set(float64(n))
}

// Running returns the number of daemon processes currently running.
func (s *Supervisor) Running() int { return int(s.running.Load()) }

// WaitReady blocks until the daemon on the first pooled port answers a probe,
// timeout elapses, or ctx ends. It returns whether the daemon became ready. Not
// becoming ready is not fatal: conversions use the cold path until a daemon
// serves, which /readyz and convert_fallback_total make visible.
func (s *Supervisor) WaitReady(ctx context.Context, timeout time.Duration) bool {
	if len(s.daemons) == 0 {
		return false
	}
	port := s.daemons[0].port
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if DaemonsUp(s.host, []string{port}, daemonDialTimeout) == 1 && s.probe(ctx, port) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(readyPollInterval):
		}
	}
	return false
}

// Recycle kills the daemon on port together with its LibreOffice so its loop
// starts a fresh one immediately. It is how tryUnoconvert abandons a conversion
// that overran or whose job was cancelled, without leaving the daemon busy with
// it. Returns false if no daemon is running on port.
func (s *Supervisor) Recycle(port string) bool {
	for _, d := range s.daemons {
		if d.port != port {
			continue
		}
		d.mu.Lock()
		pgid := d.pgid
		if pgid != 0 {
			d.recycled = true
		}
		d.mu.Unlock()
		if pgid == 0 {
			return false
		}
		if err := killGroup(pgid); err != nil {
			slog.Warn("recycling unoserver daemon failed", "port", port, "pgid", pgid, "error", err)
			return false
		}
		slog.Warn("recycled unoserver daemon after abandoned conversion", "port", port, "pgid", pgid)
		return true
	}
	return false
}

// Stop ends supervision: no more restarts, TERM to every daemon group, then KILL
// after stopGrace. Call it only after the worker has drained, so no conversion
// is cut short. Safe to call more than once and on a supervisor never started.
func (s *Supervisor) Stop() {
	if !s.stopping.CompareAndSwap(false, true) {
		return
	}
	activeSupervisor.CompareAndSwap(s, nil)
	if s.cancel != nil {
		s.cancel()
	}
	groups := s.groups()
	for _, g := range groups {
		_ = termGroup(g)
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return
	case <-time.After(stopGrace):
	}
	// unoserver waits forever on oosplash after TERM; KILL the groups so the
	// loops' Wait returns.
	for _, g := range groups {
		_ = killGroup(g)
	}
	select {
	case <-done:
	case <-time.After(stopGrace):
		slog.Warn("unoserver daemons did not exit after KILL")
	}
}

// groups returns the process groups of the daemons currently running.
func (s *Supervisor) groups() []int {
	var out []int
	for _, d := range s.daemons {
		d.mu.Lock()
		if d.pgid != 0 {
			out = append(out, d.pgid)
		}
		d.mu.Unlock()
	}
	return out
}
