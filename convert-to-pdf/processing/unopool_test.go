package processing

import (
	"context"
	"testing"
	"time"
)

func TestUnoserverPortsDefault(t *testing.T) {
	t.Setenv("UNOSERVER_PORT", "2002")
	t.Setenv("UNOSERVER_INSTANCES", "")
	t.Setenv("WORKER_CONCURRENCY", "")
	// Unset instances default to WORKER_CONCURRENCY (2) + 1 = 3 daemons, so the
	// pool always has a warm spare beyond the concurrent job count.
	ports := UnoserverPorts()
	if len(ports) != 3 || ports[0] != "2002" || ports[1] != "2003" || ports[2] != "2004" {
		t.Fatalf("default ports = %v, want [2002 2003 2004]", ports)
	}
}

func TestUnoserverInstancesTracksConcurrency(t *testing.T) {
	t.Setenv("UNOSERVER_INSTANCES", "")
	t.Setenv("WORKER_CONCURRENCY", "4")
	if got := unoserverInstances(); got != 5 {
		t.Errorf("instances with concurrency=4 = %d, want 5 (concurrency+1)", got)
	}
}

func TestUnoserverPortsCustom(t *testing.T) {
	t.Setenv("UNOSERVER_PORT", "3000")
	t.Setenv("UNOSERVER_INSTANCES", "3")
	ports := UnoserverPorts()
	want := []string{"3000", "3001", "3002"}
	if len(ports) != 3 {
		t.Fatalf("got %v, want %v", ports, want)
	}
	for i := range want {
		if ports[i] != want[i] {
			t.Fatalf("ports[%d] = %s, want %s", i, ports[i], want[i])
		}
	}
}

func TestUnoserverInstancesFloors(t *testing.T) {
	t.Setenv("UNOSERVER_INSTANCES", "0")
	if got := unoserverInstances(); got != 1 {
		t.Errorf("instances(0) = %d, want 1", got)
	}
	t.Setenv("UNOSERVER_INSTANCES", "-5")
	if got := unoserverInstances(); got != 1 {
		t.Errorf("instances(-5) = %d, want 1", got)
	}
	// A non-numeric value is treated as unset → WORKER_CONCURRENCY(2)+1 = 3.
	t.Setenv("UNOSERVER_INSTANCES", "garbage")
	t.Setenv("WORKER_CONCURRENCY", "")
	if got := unoserverInstances(); got != 3 {
		t.Errorf("instances(garbage) = %d, want 3 (default concurrency+1)", got)
	}
}

func TestPoolAcquireReleaseBounds(t *testing.T) {
	p := &unoserverPool{host: "127.0.0.1", ports: make(chan string, 2)}
	p.ports <- "2002"
	p.ports <- "2003"

	ctx := context.Background()
	a, ok := p.acquire(ctx)
	if !ok {
		t.Fatal("first acquire should succeed")
	}
	b, ok := p.acquire(ctx)
	if !ok {
		t.Fatal("second acquire should succeed")
	}
	if a == b {
		t.Fatalf("acquired the same port twice: %s", a)
	}

	// Pool is now empty: a third acquire must block until a release happens.
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, ok := p.acquire(cctx); ok {
		t.Fatal("acquire on empty pool should block and then fail on ctx timeout")
	}

	// Release one and confirm the next acquire unblocks.
	p.release(a)
	got, ok := p.acquire(ctx)
	if !ok || got != a {
		t.Fatalf("after release, acquire = (%s,%v), want (%s,true)", got, ok, a)
	}
}
