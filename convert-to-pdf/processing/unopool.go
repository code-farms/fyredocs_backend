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

var pool = newUnoserverPool()

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

// unoserverInstances reads UNOSERVER_INSTANCES, defaulting to 2 to match the
// default WORKER_CONCURRENCY for convert-to-pdf. Values < 1 fall back to 1.
func unoserverInstances() int {
	n, err := strconv.Atoi(os.Getenv("UNOSERVER_INSTANCES"))
	if err != nil || n < 1 {
		if err != nil {
			return 2
		}
		return 1
	}
	return n
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
