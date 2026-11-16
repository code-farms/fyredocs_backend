// Command bench-engine is the engine benchmark harness (Q13): it generates the
// corpus, verifies it, runs every corpus file through the real stack with
// differential checks, and compares runs. Design: docs/developer/architecture/
// next-generation-document-engine/16-benchmarking-and-testing.md §5; usage:
// scripts/bench-engine/corpus/README.md and `make bench-engine`.
//
// It runs inside fyredocs-test-binaries (the oracles live there), attached to
// the stack's network, with the Docker socket mounted for container stats.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"

	"bench-engine/internal/corpus"
	"bench-engine/internal/metrics"
	"bench-engine/internal/report"
	"bench-engine/internal/runner"
)

func main() {
	log.SetFlags(log.Ltime)
	api.DisableConfigDir()
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = cmdGen(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "compare":
		err = cmdCompare(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		log.Fatal(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: bench-engine gen|verify|run|compare [flags]  (-h on each for flags)")
	os.Exit(2)
}

// common flags shared by gen, verify and run.
type common struct{ repo, manifest, out string }

func (c *common) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.repo, "repo", ".", "repository root (fyredocs_backend)")
	fs.StringVar(&c.manifest, "manifest", "scripts/bench-engine/corpus/manifest.json", "manifest, relative to -repo")
	fs.StringVar(&c.out, "out", "scripts/bench-engine/corpus/out", "generated corpus directory, relative to -repo")
}

func (c *common) paths() (manifest, out string) {
	return filepath.Join(c.repo, c.manifest), filepath.Join(c.repo, c.out)
}

func cmdGen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	var c common
	c.bind(fs)
	force := fs.Bool("force", false, "regenerate files that already exist")
	only := fs.String("series", "", "comma-separated series IDs to (re)generate; default all")
	fs.Parse(args)
	mpath, out := c.paths()
	gens := corpus.Generators()
	m, err := corpus.Load(mpath, gens)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	lock, err := corpus.ReadLock(out)
	if err != nil {
		lock = corpus.Lock{}
	}
	scratch, err := os.MkdirTemp("", "bench-gen-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	g := &corpus.Gen{OutDir: out, RepoDir: c.repo, Scratch: scratch}
	want := splitSet(*only)
	var failed int
	for _, it := range m.Items() {
		if len(want) > 0 && !want[it.Series.ID] {
			continue
		}
		if e, ok := lock[it.ID]; ok && !*force {
			if _, err := os.Stat(filepath.Join(out, e.Path)); err == nil {
				continue
			}
		}
		rel, err := gens[it.Series.Generator](context.Background(), g, it)
		if err != nil {
			log.Printf("FAIL %s: %v", it.ID, err)
			failed++
			continue
		}
		sum, n, err := corpus.HashFile(filepath.Join(out, rel))
		if err != nil {
			return err
		}
		lock[it.ID] = corpus.LockEntry{Path: rel, SHA256: sum, Bytes: n, Category: it.Series.Category}
		log.Printf("ok   %s (%d KB)", it.ID, n/1024)
	}
	if err := corpus.WriteLock(out, lock); err != nil {
		return err
	}
	log.Printf("%d files locked, %d failed, pending series: %v", len(lock), failed, m.Pending())
	if failed > 0 {
		return fmt.Errorf("%d files failed to generate", failed)
	}
	return nil
}

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	var c common
	c.bind(fs)
	fs.Parse(args)
	mpath, out := c.paths()
	m, err := corpus.Load(mpath, corpus.Generators())
	if err != nil {
		return err
	}
	lock, err := corpus.ReadLock(out)
	if err != nil {
		return fmt.Errorf("no corpus lock (run gen first): %w", err)
	}
	if errs := corpus.Verify(out, lock, m.Items()); len(errs) > 0 {
		for _, e := range errs {
			log.Print(e)
		}
		return fmt.Errorf("corpus verify: %d problems", len(errs))
	}
	log.Printf("corpus ok: %d files match %s", len(lock), corpus.LockFile)
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var c common
	c.bind(fs)
	origin := fs.String("origin", "http://localhost", "public origin the stack signs URLs with")
	edge := fs.String("edge", "caddy:80", "address to dial for -origin from inside the stack network ('' = dial directly)")
	email := fs.String("email", "bench-engine@example.com", "bench account (signed up if missing)")
	password := fs.String("password", "Bench-engine-pass-1", "bench account password")
	runs := fs.Int("runs", 1, "passes per job (3 for a baseline)")
	tools := fs.String("tools", "", "comma-separated tools to run; default all")
	series := fs.String("series", "", "comma-separated series IDs; default all")
	limit := fs.Int("limit", 0, "max items per series (0 = all)")
	timeout := fs.Duration("job-timeout", 15*time.Minute, "per-job timeout")
	socket := fs.String("docker", "/var/run/docker.sock", "Docker Engine socket for container stats ('' = off)")
	prefix := fs.String("container-prefix", "fyredocs-", "worker container name = prefix + service + suffix")
	suffix := fs.String("container-suffix", "-1", "")
	keep := fs.String("keep", "", "keep the run's downloads and oracle files in this directory (inside the repo) instead of a temp dir")
	allowCache := fs.Bool("allow-cache", false, "run even if a worker's result cache is on (numbers then include cache hits)")
	startup := fs.Bool("startup", false, "also measure cold start of the four workers (restarts them)")
	host := fs.String("host", "", "host label for the report (default: hostname, marked dev)")
	results := fs.String("results", "docs/developer/architecture/next-generation-document-engine/benchmarks/results", "report directory, relative to -repo")
	fs.Parse(args)

	mpath, out := c.paths()
	m, err := corpus.Load(mpath, corpus.Generators())
	if err != nil {
		return err
	}
	lock, err := corpus.ReadLock(out)
	if err != nil {
		return fmt.Errorf("no corpus lock (run gen first): %w", err)
	}
	if errs := corpus.Verify(out, lock, m.Items()); len(errs) > 0 {
		return fmt.Errorf("corpus changed since gen (%d problems; run verify)", len(errs))
	}
	cl, err := runner.NewClient(*origin, *edge)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := cl.Login(ctx, *email, *password); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	var d *metrics.Docker
	if *socket != "" {
		d = metrics.NewDocker(*socket)
	}
	scratch := *keep
	if scratch == "" {
		if scratch, err = os.MkdirTemp("", "bench-run-"); err != nil {
			return err
		}
		defer os.RemoveAll(scratch)
	} else if err := os.MkdirAll(scratch, 0o755); err != nil {
		return err
	}

	toolSet, seriesSet := splitSet(*tools), splitSet(*series)
	perSeries := map[string]int{}
	var items []corpus.Item
	for _, it := range m.Items() {
		if len(seriesSet) > 0 && !seriesSet[it.Series.ID] {
			continue
		}
		if *limit > 0 && perSeries[it.Series.ID] >= *limit {
			continue
		}
		perSeries[it.Series.ID]++
		items = append(items, it)
	}
	name := func(group string) string { return *prefix + group + *suffix }

	rep := &report.Report{
		Date:          time.Now().UTC().Format(time.RFC3339),
		Host:          hostLabel(*host),
		GitSHA:        gitSHA(c.repo),
		ImageDigests:  map[string]string{},
		Runs:          *runs,
		Pending:       m.Pending(),
		Note:          "Development numbers unless the host is the production-class one (doc 16 §5.3).",
		CorpusLockSHA: func() string { s, _ := corpus.LockSHA(out); return s }(),
	}
	if d != nil {
		for _, g := range []string{"convert-to-pdf", "convert-from-pdf", "organize-pdf", "optimize-pdf"} {
			rep.ImageDigests[g] = d.ImageDigest(ctx, name(g))
			// A cached worker answers repeats from Redis without running the
			// engine, so the numbers would measure the cache (compose.bench.yml
			// sets the TTL to 0).
			if ttl := d.Env(ctx, name(g))["RESULT_CACHE_TTL_SECONDS"]; ttl != "0" && !*allowCache {
				log.Fatalf("%s has its result cache on (RESULT_CACHE_TTL_SECONDS=%q); bring the stack up with scripts/bench-engine/compose.bench.yml, or pass -allow-cache", g, ttl)
			}
		}
		if *startup {
			rep.StartupMs = map[string]float64{}
			for _, g := range []string{"convert-to-pdf", "convert-from-pdf", "organize-pdf", "optimize-pdf"} {
				if dur, err := d.Restart(ctx, name(g), 3*time.Minute); err == nil {
					rep.StartupMs[g] = float64(dur.Milliseconds())
				} else {
					log.Printf("startup %s: %v", g, err)
				}
			}
		}
	}
	rep.Rows = runner.Run(ctx, cl, d, lock, items, runner.Options{
		CorpusDir: out, Scratch: scratch, Runs: *runs, JobTimeout: *timeout, ContainerName: name,
		Filter: func(_ corpus.Item, tool string) bool { return len(toolSet) == 0 || toolSet[tool] },
	})
	base := filepath.Join(c.repo, *results, "bench-engine-"+time.Now().UTC().Format("20060102-150405"))
	if err := report.Write(base, rep); err != nil {
		return err
	}
	log.Printf("report: %s.{json,md} (%d jobs)", base, len(rep.Rows))
	return nil
}

func cmdCompare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	noise := fs.Bool("noise", false, "the two reports are runs of the SAME code: report the noise floor")
	wall := fs.Float64("wall-pct", report.DefaultThresholds.WallP50Pct, "wall p50 regression threshold, percent")
	ssim := fs.Float64("ssim-drop", report.DefaultThresholds.SSIMDrop, "SSIM regression threshold")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: compare [flags] base.json candidate.json")
	}
	a, err := report.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	b, err := report.Load(fs.Arg(1))
	if err != nil {
		return err
	}
	if a.CorpusLockSHA != b.CorpusLockSHA {
		log.Printf("WARNING: different corpora (%s vs %s): the comparison is not like for like", a.CorpusLockSHA, b.CorpusLockSHA)
	}
	if *noise {
		pct, where := report.Noise(a, b)
		fmt.Printf("noise floor: %.1f%% wall p50 (largest at %s); doc 16 target ±5%%\n", pct, where)
		return nil
	}
	changes := report.Compare(a, b, report.Thresholds{WallP50Pct: *wall, SSIMDrop: *ssim})
	for _, c := range changes {
		fmt.Printf("%-11s %s: %s\n", c.Kind, c.Where, c.Detail)
	}
	if report.HasRegression(changes) {
		return fmt.Errorf("regressions found")
	}
	fmt.Println("no regressions")
	return nil
}

func splitSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			m[p] = true
		}
	}
	return m
}

func hostLabel(h string) string {
	if h != "" {
		return h
	}
	n, _ := os.Hostname()
	return n + " (dev)"
}

func gitSHA(repo string) string {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	sha := strings.TrimSpace(string(out))
	// Uncommitted changes (corpus/out is gitignored) mean the numbers are not
	// reproducible from the SHA alone.
	if st, err := exec.Command("git", "-C", repo, "status", "--porcelain", "--untracked-files=no").Output(); err == nil && len(strings.TrimSpace(string(st))) > 0 {
		sha += "-dirty"
	}
	return sha
}
