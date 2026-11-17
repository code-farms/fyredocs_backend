package jobslots

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestFromEnvDefaultsAndOverrides(t *testing.T) {
	t.Setenv("CPU_JOB_SLOTS", "")
	t.Setenv("IO_JOB_SLOTS", "")
	s := FromEnv("test")
	if s.Capacity(CPU) != DefaultCPU() || s.Capacity(IO) != 2*DefaultCPU() {
		t.Fatalf("defaults cpu=%d io=%d, want %d and %d", s.Capacity(CPU), s.Capacity(IO), DefaultCPU(), 2*DefaultCPU())
	}
	t.Setenv("CPU_JOB_SLOTS", "3")
	t.Setenv("IO_JOB_SLOTS", "5")
	s = FromEnv("test")
	if s.Capacity(CPU) != 3 || s.Capacity(IO) != 5 || s.Total() != 8 {
		t.Fatalf("overrides cpu=%d io=%d total=%d", s.Capacity(CPU), s.Capacity(IO), s.Total())
	}
	t.Setenv("CPU_JOB_SLOTS", "0")
	t.Setenv("IO_JOB_SLOTS", "-2")
	s = FromEnv("test")
	if s.Capacity(CPU) != DefaultCPU() || s.Capacity(IO) != 2*DefaultCPU() {
		t.Fatalf("invalid values must fall back to defaults, got cpu=%d io=%d", s.Capacity(CPU), s.Capacity(IO))
	}
}

// The point of the package: a full cpu pool must not delay io work.
func TestClassesAreIndependent(t *testing.T) {
	s := New("test", 1, 1)
	releaseCPU, err := s.Acquire(context.Background(), CPU, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseCPU()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	releaseIO, err := s.Acquire(ctx, IO, 1)
	if err != nil {
		t.Fatalf("io acquire blocked by a held cpu slot: %v", err)
	}
	releaseIO()
}

func TestAcquireBlocksAtCapacityAndCancels(t *testing.T) {
	s := New("test", 2, 1)
	r1, _ := s.Acquire(context.Background(), CPU, 1)
	r2, _ := s.Acquire(context.Background(), CPU, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := s.Acquire(ctx, CPU, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third cpu acquire on a 2-slot pool: err %v, want deadline exceeded", err)
	}

	got := make(chan struct{})
	go func() {
		r, err := s.Acquire(context.Background(), CPU, 1)
		if err == nil {
			r()
		}
		close(got)
	}()
	r1()
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("waiter not woken after a release")
	}
	r2()
}

func TestWeightIsClampedAndReleaseIdempotent(t *testing.T) {
	s := New("test", 2, 2)
	// A weight above capacity must not deadlock: it takes the whole pool.
	r, err := s.Acquire(context.Background(), CPU, 10)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := s.Acquire(ctx, CPU, 1); err == nil {
		t.Fatal("pool should be full after a clamped weight-10 acquire")
	}
	r()
	r() // a second call must not release tokens it does not hold
	r1, _ := s.Acquire(context.Background(), CPU, 1)
	r2, _ := s.Acquire(context.Background(), CPU, 1)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	if _, err := s.Acquire(ctx2, CPU, 1); err == nil {
		t.Fatal("double release over-filled the pool")
	}
	r1()
	r2()
}

func TestConcurrencyNeverExceedsCapacity(t *testing.T) {
	s := New("test", 3, 1)
	var running, peak atomic.Int32
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			r, err := s.Acquire(context.Background(), CPU, 1)
			if err != nil {
				return
			}
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
			r()
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	if p := peak.Load(); p > 3 {
		t.Fatalf("peak concurrency %d exceeds capacity 3", p)
	}
}

func TestPerUnitTakesNothingAndContextCarriesSlots(t *testing.T) {
	s := New("test", 1, 1)
	release, err := s.Acquire(context.Background(), PerUnit, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// The cpu pool must still be free: a PerUnit job holds no token.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r, err := s.Acquire(ctx, CPU, 1)
	if err != nil {
		t.Fatalf("PerUnit job held a cpu token: %v", err)
	}
	r()

	if FromContext(context.Background()) != nil {
		t.Fatal("empty context must carry no slots")
	}
	if got := FromContext(WithSlots(context.Background(), s)); got != s {
		t.Fatal("WithSlots/FromContext round trip failed")
	}
	if WithSlots(context.Background(), nil) == nil {
		t.Fatal("WithSlots(nil) must return the context unchanged")
	}
}
