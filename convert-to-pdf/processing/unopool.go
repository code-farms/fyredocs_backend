package processing

import (
	"context"
	"os"
	"strconv"
)

// unoserverPool bounds concurrent unoconvert calls to the number of running
// unoserver daemons and spreads work across them. Without it, every Office→PDF
// conversion serializes on a single daemon port regardless of worker
// concurrency. The entrypoint launches UNOSERVER_INSTANCES daemons on
// consecutive ports starting at UNOSERVER_PORT; this pool hands those ports out
// round-robin, blocking when all are busy.
type unoserverPool struct {
	host  string
	ports chan string
}

// pool is the process-wide daemon port pool, built at init. Package-level because
// the port set is fixed for the container's lifetime and every conversion must
// draw from the same pool for the bound to mean anything.
var pool = newUnoserverPool()

// newUnoserverPool builds the port pool at package init, pre-filling the channel
// with one token per daemon port so a buffered-channel receive is the whole
// acquire path. Nothing here contacts the daemons — a port in the pool is a claim
// that entrypoint.sh launched one, not a verified connection.
func newUnoserverPool() *unoserverPool {
	host := envOrDefault("UNOSERVER_HOST", "127.0.0.1")
	ports := UnoserverPorts()
	ch := make(chan string, len(ports))
	for _, p := range ports {
		ch <- p
	}
	return &unoserverPool{host: host, ports: ch}
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

// unoserverInstances reads UNOSERVER_INSTANCES. When set, it is honored
// (values < 1 clamp to 1). When unset, it defaults to WORKER_CONCURRENCY + 1 so
// there is one warm daemon per concurrent job plus a spare — keeping the slow
// cold-start LibreOffice fallback effectively unreachable even as concurrency
// is raised on larger hosts.
//
// NOTE: entrypoint.sh computes the same count independently and defaults to 2,
// NOT to WORKER_CONCURRENCY + 1. With UNOSERVER_INSTANCES unset the two disagree
// and this pool hands out a port no daemon is listening on; the only symptom is
// conversions silently taking the slow fallback. See the header comment in
// convert-to-pdf/entrypoint.sh. Pin UNOSERVER_INSTANCES to keep them aligned.
func unoserverInstances() int {
	if v := os.Getenv("UNOSERVER_INSTANCES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 1 {
				return 1
			}
			return n
		}
	}
	return workerConcurrency() + 1
}

// workerConcurrency mirrors the worker package's WORKER_CONCURRENCY default (2)
// without importing it, so the daemon pool can size itself to the job
// concurrency. Read here rather than imported to keep the processing package
// independent of the worker package.
func workerConcurrency() int {
	if v := os.Getenv("WORKER_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 2
}

// UnoserverPorts returns the consecutive ports the unoserver daemons listen on
// (UNOSERVER_PORT base, count = UNOSERVER_INSTANCES). The Go pool and the health
// check both derive their port list from this single source of truth so they
// always agree with what the entrypoint launched.
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
