package processing

import (
	"context"
	"fyredocs/shared/jobslots"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// unoserverPool bounds concurrent unoconvert calls to the number of running
// unoserver daemons and spreads work across them. Without it, every Office→PDF
// conversion serializes on a single daemon port regardless of worker
// concurrency. The Supervisor (daemons.go) launches one daemon per port in
// UnoserverPorts(); this pool hands those same ports out round-robin, blocking
// when all are busy.
type unoserverPool struct {
	host  string
	ports chan string

	mu sync.Mutex
	// uses counts successful conversions per port since that port's daemon
	// last recycled; it drives recycling by use (recycleCause).
	uses map[string]int
}

// pool is the process-wide daemon port pool, built at init. Package-level because
// the port set is fixed for the container's lifetime and every conversion must
// draw from the same pool for the bound to mean anything.
var pool = newUnoserverPool()

// newUnoserverPool builds the port pool at package init, pre-filling the channel
// with one token per daemon port so a buffered-channel receive is the whole
// acquire path. Nothing here contacts the daemons — a port in the pool is a claim
// that the Supervisor runs one, not a verified connection (tryUnoconvert dials
// before use).
func newUnoserverPool() *unoserverPool {
	host := envOrDefault("UNOSERVER_HOST", "127.0.0.1")
	ports := UnoserverPorts()
	ch := make(chan string, len(ports))
	for _, p := range ports {
		ch <- p
	}
	return &unoserverPool{host: host, ports: ch, uses: map[string]int{}}
}

// acquire blocks until a daemon port is free or ctx is cancelled.
func (p *unoserverPool) acquire(ctx context.Context) (string, bool) {
	select {
	case port := <-p.ports:
		return port, true
	case <-ctx.Done():
		return "", false
	}
}

// release returns a port to the pool for reuse.
func (p *unoserverPool) release(port string) {
	p.ports <- port
}

// recordUse counts one successful conversion on port and returns the count
// since the port's daemon last recycled.
func (p *unoserverPool) recordUse(port string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.uses[port]++
	return p.uses[port]
}

// resetUses zeroes port's count; called when its daemon is recycled.
func (p *unoserverPool) resetUses(port string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.uses, port)
}

// unoserverInstances reads UNOSERVER_INSTANCES. When set, it is honored
// (values < 1 clamp to 1). When unset, it defaults to the cpu job slots + 1
// (CPU_JOB_SLOTS, shared/jobslots): one warm daemon per LibreOffice job that can
// run at once plus a spare, which keeps the slow cold-start fallback effectively
// unreachable. It followed WORKER_CONCURRENCY until 2026-11-16; that now counts
// jobs in flight across both classes, including image-to-pdf (io), which needs
// no daemon. Each daemon costs memory (deploy.sh budgets ~300 MB), so deploy.sh
// sizes CPU_JOB_SLOTS to fit the container's cap.
func unoserverInstances() int {
	if v := os.Getenv("UNOSERVER_INSTANCES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 1 {
				return 1
			}
			return n
		}
	}
	return jobslots.CPUFromEnv() + 1
}

// UnoserverPorts returns the consecutive ports the unoserver daemons listen on
// (UNOSERVER_PORT base, count = UNOSERVER_INSTANCES). The Go pool and the health
// check and the Supervisor all derive their port list from this single source
// of truth, so the daemons launched and the ports handed out always agree.
func UnoserverPorts() []string {
	base, err := strconv.Atoi(envOrDefault("UNOSERVER_PORT", "2002"))
	if err != nil {
		base = 2002
	}
	n := unoserverInstances()
	ports := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ports = append(ports, strconv.Itoa(base+i))
	}
	return ports
}

// UnoserverHost returns the configured unoserver bind host.
func UnoserverHost() string {
	return envOrDefault("UNOSERVER_HOST", "127.0.0.1")
}

// DaemonsUp returns how many of ports accept a TCP connection on host within
// timeout each. A listening port is taken as a live daemon: unoserver only binds
// its XML-RPC port once LibreOffice has started, and a crashed daemon releases it.
// Dials run sequentially, so the worst case is len(ports) × timeout; keep the
// timeout short when calling from a probe.
func DaemonsUp(host string, ports []string, timeout time.Duration) int {
	up := 0
	for _, p := range ports {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, p), timeout)
		if err == nil {
			conn.Close()
			up++
		}
	}
	return up
}
