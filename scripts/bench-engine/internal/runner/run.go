package runner

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bench-engine/internal/check"
	"bench-engine/internal/corpus"
	"bench-engine/internal/metrics"
	"bench-engine/internal/report"
)

// Options configures one benchmark run.
type Options struct {
	CorpusDir     string        // generated corpus (with corpus.lock)
	Scratch       string        // downloads and oracle intermediates
	Runs          int           // passes per job; checks run on the first
	JobTimeout    time.Duration // per job, upload to download
	ContainerName func(group string) string
	Filter        func(it corpus.Item, tool string) bool
	// Concurrency is the number of jobs in flight (default 1). Above 1 the run
	// measures contention (e.g. whether merges wait behind scans) and the
	// per-job container samples are skipped: the container is shared, so they
	// would not belong to one job.
	Concurrency int
}

// Run executes every item × tool × run against the stack and returns the rows,
// in corpus order. With Concurrency 1 (the default) one job is in flight at a
// time, so the worker's sampled resources belong to that job (doc 16 §5.3
// measures a job, not a load test; k6 covers load).
func Run(ctx context.Context, c *Client, d *metrics.Docker, lock corpus.Lock, items []corpus.Item, o Options) []report.Row {
	type task struct {
		it        corpus.Item
		tool      string
		file      string
		bytes     int64
		run, slot int
	}
	var tasks []task
	for _, it := range items {
		e, ok := lock[it.ID]
		if !ok {
			continue
		}
		file := filepath.Join(o.CorpusDir, e.Path)
		for _, tool := range it.Series.Tools {
			if o.Filter != nil && !o.Filter(it, tool) {
				continue
			}
			for run := 1; run <= o.Runs; run++ {
				tasks = append(tasks, task{it: it, tool: tool, file: file, bytes: e.Bytes, run: run, slot: len(tasks)})
			}
		}
	}

	n := max(1, o.Concurrency)
	sampler := d
	if n > 1 {
		sampler = nil
	}
	rows := make([]report.Row, len(tasks))
	next := make(chan task)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range next {
				row := runOne(ctx, c, sampler, t.it, t.tool, t.file, t.bytes, t.run, o)
				peak := "n/a"
				if row.Resources.Frames > 0 {
					peak = fmt.Sprintf("%.0fMiB", row.Resources.PeakMiB)
				}
				log.Printf("%-28s %-18s run %d: %s wall=%dms server=%dms peak=%s", t.it.ID, t.tool, t.run, row.Status, row.WallMs, row.ServerMs, peak)
				rows[t.slot] = row
			}
		}()
	}
	for _, t := range tasks {
		next <- t
	}
	close(next)
	wg.Wait()
	return rows
}

// runOne runs a single job and, on the first run, its checks. Harness-side
// failures (upload, network, timeout) are status "error" so they are never
// confused with the tool rejecting the input ("failed").
func runOne(ctx context.Context, c *Client, d *metrics.Docker, it corpus.Item, tool, file string, size int64, run int, o Options) report.Row {
	row := report.Row{Item: it.ID, Category: it.Series.Category, Tool: tool, Run: run, InputBytes: size, Expected: it.Series.Expect}
	group, _ := Group(tool)
	ctx, cancel := context.WithTimeout(ctx, o.JobTimeout)
	defer cancel()

	var sampler *metrics.Sampler
	if d != nil && o.ContainerName != nil {
		if s, err := d.Start(ctx, o.ContainerName(group)); err == nil {
			sampler = s
		}
	}
	start := time.Now()
	inputs := []string{file}
	if tool == "merge-pdf" {
		inputs = []string{file, file}
	}
	var ids []string
	for _, in := range inputs {
		id, err := c.Upload(ctx, in)
		if err != nil {
			return finish(row, sampler, "error", err)
		}
		ids = append(ids, id)
	}
	job, err := c.CreateJob(ctx, tool, ids, DefaultOptions(tool, it.Series.Params))
	if err != nil {
		// A 4xx at job creation is the service refusing the input (e.g. a file
		// with no %PDF header sniffed as application/octet-stream): a clean,
		// correct failure, not a harness fault.
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500 && apiErr.Status != 401 && apiErr.Status != 429 {
			row.WallMs = time.Since(start).Milliseconds()
			return finish(row, sampler, "rejected", err)
		}
		return finish(row, sampler, "error", err)
	}
	job, err = c.Wait(ctx, tool, job.ID, 250*time.Millisecond)
	if err != nil {
		return finish(row, sampler, "error", fmt.Errorf("waiting: %w", err))
	}
	if job.CompletedAt != nil {
		row.ServerMs = job.CompletedAt.Sub(job.CreatedAt).Milliseconds()
	}
	if job.Status != "completed" {
		row.WallMs = time.Since(start).Milliseconds()
		return finish(row, sampler, "failed", fmt.Errorf("%s", job.FailureReason))
	}
	ext := filepath.Ext(job.OutputName)
	if ext == "" {
		ext = ".bin"
	}
	out := filepath.Join(o.Scratch, "outputs", fmt.Sprintf("%s.%s.r%d%s", it.ID, tool, run, ext))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return finish(row, sampler, "error", err)
	}
	if err := c.Download(ctx, tool, job.ID, out); err != nil {
		return finish(row, sampler, "error", err)
	}
	row.WallMs = time.Since(start).Milliseconds()
	row = finish(row, sampler, "completed", nil)
	if fi, err := os.Stat(out); err == nil {
		row.OutputBytes = fi.Size()
	}
	if run == 1 {
		row.Checks = check.Run(context.Background(), check.Input{
			Tool: tool, Category: it.Series.Category, Inputs: inputs, Output: out,
			Password: it.Series.Params["password"], SourceText: check.SourceTextFor(file), Scratch: o.Scratch,
		})
	}
	return row
}

// finish stops the sampler, records resources and the outcome.
func finish(row report.Row, s *metrics.Sampler, status string, err error) report.Row {
	if s != nil {
		row.Resources = s.Stop()
	}
	row.Status = status
	if err != nil {
		row.Error = strings.TrimSpace(err.Error())
	}
	return row
}
