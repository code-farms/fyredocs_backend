// Package jobslots bounds how much work of each class a worker runs at once.
//
// A document worker does two kinds of job. "cpu" jobs run a CPU-bound engine
// (LibreOffice, Ghostscript, Tesseract, Python, Go image cleanup) and are limited
// by the container's CPU limit. "io" jobs are cheap pdfcpu operations (merge,
// split, rotate, metadata, image import) whose cost is mostly object-storage
// transfer. With one shared slot pool, a burst of slow cpu jobs used every slot
// and a 300 ms merge waited behind four scan clean-ups. Giving each class its own
// pool lets cheap work keep flowing (docs/developer/architecture/
// next-generation-document-engine/12-performance-architecture.md §5.1).
//
// Capacities:
//
//	CPU_JOB_SLOTS  default runtime.GOMAXPROCS(0): since Go 1.25 that follows the
//	               container's CPU limit, so a 2-CPU worker runs 2 cpu jobs.
//	IO_JOB_SLOTS   default 2 × CPU_JOB_SLOTS.
//
// The cpu pool is weighted so a job's inner parallelism can draw from it too:
// optimize-pdf's OCR takes one cpu token per page it is recognising, so two OCR
// jobs can no longer start eight tesseracts on two CPUs.
package jobslots

import (
	"context"
	"runtime"
	"time"

	"fyredocs/shared/config"
	"fyredocs/shared/metrics"
	"golang.org/x/sync/semaphore"
)

// Class is a workload class.
type Class string

const (
	// CPU is work bounded by the CPU limit: external engines and Go image work.
	CPU Class = "cpu"
	// IO is cheap pdfcpu work whose time is mostly transfers.
	IO Class = "io"
	// PerUnit marks a job that takes no slot itself because it draws cpu tokens
	// for each unit of inner work (optimize-pdf OCR: one per page, through
	// FromContext). Holding a job token while the pages wait for more would
	// deadlock once every token is held by such jobs.
	PerUnit Class = "per-unit"
)

// Slots holds one weighted semaphore per class for one worker process.
type Slots struct {
	service string
	cpu     *semaphore.Weighted
	io      *semaphore.Weighted
	cpuCap  int64
	ioCap   int64
}

// FromEnv builds the pools from CPU_JOB_SLOTS and IO_JOB_SLOTS (values below 1
// fall back to the defaults). service labels the metrics.
func FromEnv(service string) *Slots {
	cpu := CPUFromEnv()
	io := config.GetEnvInt("IO_JOB_SLOTS", 0)
	if io < 1 {
		io = 2 * cpu
	}
	return New(service, cpu, io)
}

// CPUFromEnv is the cpu capacity: CPU_JOB_SLOTS, or DefaultCPU when it is unset
// or below 1. Exported for sizing that follows it (convert-to-pdf keeps one warm
// LibreOffice daemon per cpu slot plus a spare).
func CPUFromEnv() int {
	if n := config.GetEnvInt("CPU_JOB_SLOTS", 0); n >= 1 {
		return n
	}
	return DefaultCPU()
}

// DefaultCPU is the cpu capacity used when CPU_JOB_SLOTS is unset.
func DefaultCPU() int {
	return max(1, runtime.GOMAXPROCS(0))
}

// New builds pools with explicit capacities (each at least 1).
func New(service string, cpu, io int) *Slots {
	cpu, io = max(1, cpu), max(1, io)
	return &Slots{
		service: service,
		cpu:     semaphore.NewWeighted(int64(cpu)),
		io:      semaphore.NewWeighted(int64(io)),
		cpuCap:  int64(cpu),
		ioCap:   int64(io),
	}
}

// Capacity reports the size of a class's pool.
func (s *Slots) Capacity(c Class) int {
	if c == IO {
		return int(s.ioCap)
	}
	return int(s.cpuCap)
}

// Total is the sum of both capacities: the default number of jobs a worker
// keeps in flight, so each class can fill its own pool.
func (s *Slots) Total() int { return int(s.cpuCap + s.ioCap) }

type ctxKey struct{}

// WithSlots returns ctx carrying s, for processing code that draws tokens for
// its own inner work (see PerUnit).
func WithSlots(ctx context.Context, s *Slots) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, s)
}

// FromContext returns the Slots set by WithSlots, or nil (tests, tools run
// outside a worker), in which case callers run unbounded as before.
func FromContext(ctx context.Context) *Slots {
	s, _ := ctx.Value(ctxKey{}).(*Slots)
	return s
}

// Acquire waits for weight tokens of class c (clamped to 1..capacity) and
// returns the function that gives them back. It returns ctx's error if ctx ends
// first; nothing is held then. The wait is recorded in job_slot_wait_seconds.
func (s *Slots) Acquire(ctx context.Context, c Class, weight int) (func(), error) {
	if c == PerUnit {
		return func() {}, nil
	}
	sem, capacity := s.cpu, s.cpuCap
	if c == IO {
		sem, capacity = s.io, s.ioCap
	} else {
		c = CPU
	}
	w := min(max(int64(weight), 1), capacity)

	start := time.Now()
	if err := sem.Acquire(ctx, w); err != nil {
		return nil, err
	}
	metrics.JobSlotWait.WithLabelValues(s.service, string(c)).Observe(time.Since(start).Seconds())
	inUse := metrics.JobSlotsInUse.WithLabelValues(s.service, string(c))
	inUse.Add(float64(w))

	released := false
	return func() {
		if released {
			return
		}
		released = true
		inUse.Sub(float64(w))
		sem.Release(w)
	}, nil
}
