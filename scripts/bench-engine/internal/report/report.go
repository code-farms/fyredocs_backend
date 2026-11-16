// Package report stores benchmark results (doc 16 §5.4: one JSON document per
// run plus a Markdown table) and compares two runs (§5.5).
package report

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"bench-engine/internal/check"
	"bench-engine/internal/metrics"
)

// Row is one job: one corpus item through one tool, one run.
type Row struct {
	Item        string         `json:"item"`
	Category    string         `json:"category"`
	Tool        string         `json:"tool"`
	Run         int            `json:"run"`
	Status      string         `json:"status"` // completed | failed | rejected (4xx at job creation) | error (harness-side)
	Expected    string         `json:"expected,omitempty"`
	Error       string         `json:"error,omitempty"`
	InputBytes  int64          `json:"input_bytes"`
	OutputBytes int64          `json:"output_bytes"`
	WallMs      int64          `json:"wall_ms"`   // upload start → output downloaded
	ServerMs    int64          `json:"server_ms"` // job completedAt − createdAt
	Resources   metrics.Sample `json:"resources"` // worker container, 1 s sampled, lower bound
	Checks      []check.Result `json:"checks,omitempty"`
}

// Report is one benchmark run.
type Report struct {
	Host          string             `json:"host"`
	Date          string             `json:"date"`
	GitSHA        string             `json:"git_sha"`
	ImageDigests  map[string]string  `json:"image_digests"`
	CorpusLockSHA string             `json:"corpus_lock_sha"`
	Runs          int                `json:"runs"`
	Note          string             `json:"note"`
	StartupMs     map[string]float64 `json:"startup_ms,omitempty"`
	Pending       []string           `json:"pending_series,omitempty"`
	Rows          []Row              `json:"rows"`
}

// Load reads a JSON report.
func Load(path string) (*Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	return &r, json.Unmarshal(b, &r)
}

// Write stores r as <base>.json and <base>.md.
func Write(base string, r *Report) error {
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(base+".json", append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(base+".md", []byte(Markdown(r)), 0o644)
}

// Key groups rows for summaries and comparison.
type Key struct{ Tool, Category string }

// Summary aggregates one tool × category.
type Summary struct {
	Key
	Jobs, Failed, Unexpected int
	WallP50, WallP95         float64
	ServerP50, ServerP95     float64
	PeakMiBP50, PeakMiBMax   float64
	PeakSampled              int // completed jobs with at least one stats frame
	ChecksPassed, ChecksRun  int
}

// Summarize groups r's rows by tool × category, sorted.
func Summarize(r *Report) []Summary {
	groups := map[Key][]Row{}
	for _, row := range r.Rows {
		k := Key{row.Tool, row.Category}
		groups[k] = append(groups[k], row)
	}
	var out []Summary
	for k, rows := range groups {
		s := Summary{Key: k, Jobs: len(rows)}
		var wall, server, peak []float64
		for _, row := range rows {
			if row.Status != "completed" {
				s.Failed++
				if row.Expected != "fail-or-ok" || row.Status == "error" {
					s.Unexpected++
				}
				continue
			}
			wall = append(wall, float64(row.WallMs))
			server = append(server, float64(row.ServerMs))
			if row.Resources.Frames > 0 {
				peak = append(peak, row.Resources.PeakMiB)
			}
			for _, c := range row.Checks {
				if c.NA != "" {
					continue
				}
				s.ChecksRun++
				if c.Pass {
					s.ChecksPassed++
				}
			}
		}
		s.WallP50, s.WallP95 = Percentile(wall, 50), Percentile(wall, 95)
		s.ServerP50, s.ServerP95 = Percentile(server, 50), Percentile(server, 95)
		s.PeakSampled = len(peak)
		s.PeakMiBP50 = Percentile(peak, 50)
		for _, p := range peak {
			s.PeakMiBMax = math.Max(s.PeakMiBMax, p)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tool != out[j].Tool {
			return out[i].Tool < out[j].Tool
		}
		return out[i].Category < out[j].Category
	})
	return out
}

// Percentile is the nearest-rank percentile p (0–100) of v; 0 for empty input.
func Percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	rank := int(math.Ceil(p/100*float64(len(s)))) - 1
	return s[max(0, min(rank, len(s)-1))]
}

// Markdown renders the human-readable report.
func Markdown(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Engine benchmark %s\n\n", r.Date)
	fmt.Fprintf(&b, "- Host: %s\n- Git: `%s`\n- Corpus lock: `%s`\n- Runs per job: %d\n- %s\n\n", r.Host, r.GitSHA, short(r.CorpusLockSHA), r.Runs, r.Note)
	if len(r.Pending) > 0 {
		fmt.Fprintf(&b, "Pending corpus series (no files yet): %s\n\n", strings.Join(r.Pending, ", "))
	}
	if len(r.StartupMs) > 0 {
		b.WriteString("| Cold start | ms |\n|---|---|\n")
		for _, k := range sortedKeys(r.StartupMs) {
			fmt.Fprintf(&b, "| %s | %.0f |\n", k, r.StartupMs[k])
		}
		b.WriteString("\n")
	}
	b.WriteString("Peak RAM is the worker container's working set sampled once per second: a lower bound. " +
		"Jobs shorter than the first stats frame (about 1 s) get no sample; \"n/a\" means no job of the row was sampled.\n\n")
	b.WriteString("| Tool | Category | Jobs | Failed (unexpected) | Wall p50/p95 ms | Server p50/p95 ms | Peak RAM p50/max MiB | Checks passed |\n|---|---|---|---|---|---|---|---|\n")
	for _, s := range Summarize(r) {
		peak := "n/a"
		if s.PeakSampled > 0 {
			peak = fmt.Sprintf("%.0f / %.0f", s.PeakMiBP50, s.PeakMiBMax)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d (%d) | %.0f / %.0f | %.0f / %.0f | %s | %d/%d |\n",
			s.Tool, s.Category, s.Jobs, s.Failed, s.Unexpected, s.WallP50, s.WallP95, s.ServerP50, s.ServerP95, peak, s.ChecksPassed, s.ChecksRun)
	}
	var fails []string
	for _, row := range r.Rows {
		if row.Status != "completed" && row.Expected != "fail-or-ok" {
			fails = append(fails, fmt.Sprintf("- `%s` %s: %s %s", row.Item, row.Tool, row.Status, row.Error))
		}
		for _, c := range row.Checks {
			if c.NA == "" && !c.Pass {
				fails = append(fails, fmt.Sprintf("- `%s` %s: check %s = %.4g %s", row.Item, row.Tool, c.Name, c.Value, c.Detail))
			}
		}
	}
	if len(fails) > 0 {
		fmt.Fprintf(&b, "\n## Findings (%d)\n\n%s\n", len(fails), strings.Join(fails, "\n"))
	}
	return b.String()
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}
