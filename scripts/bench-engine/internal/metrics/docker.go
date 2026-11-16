// Package metrics samples a worker container's resource use from the Docker
// Engine API while a job runs (doc 16 §5.3: peak/average RAM, CPU utilisation).
//
// It speaks the Engine API over the unix socket with net/http instead of
// pulling in the Docker SDK: the harness needs two endpoints, and the SDK would
// add dozens of modules to the workspace. Docker's stats stream reports about
// once per second, so peaks are a 1-second-sampled LOWER BOUND; the report says
// so next to every RAM figure.
package metrics

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Docker is a minimal Engine API client.
type Docker struct{ http *http.Client }

// NewDocker connects to the Engine API at socketPath (/var/run/docker.sock).
func NewDocker(socketPath string) *Docker {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socketPath)
	}}
	return &Docker{http: &http.Client{Transport: tr}}
}

// statsFrame is the subset of one /containers/{id}/stats frame used here.
type statsFrame struct {
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	CPUStats    cpuStats `json:"cpu_stats"`
	PreCPUStats cpuStats `json:"precpu_stats"`
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	SystemUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs  uint64 `json:"online_cpus"`
}

// workingSet is memory usage minus inactive page cache, the figure `docker
// stats` shows; raw usage would count reclaimable file cache as job memory.
func (f statsFrame) workingSet() uint64 {
	inactive := f.MemoryStats.Stats["inactive_file"]
	if inactive > f.MemoryStats.Usage {
		return f.MemoryStats.Usage
	}
	return f.MemoryStats.Usage - inactive
}

// cpuPercent is the container's CPU use over the frame interval in percent of
// one CPU (200 = two CPUs busy), the `docker stats` convention.
func (f statsFrame) cpuPercent() float64 {
	cpu := float64(f.CPUStats.CPUUsage.TotalUsage) - float64(f.PreCPUStats.CPUUsage.TotalUsage)
	sys := float64(f.CPUStats.SystemUsage) - float64(f.PreCPUStats.SystemUsage)
	if cpu <= 0 || sys <= 0 || f.CPUStats.OnlineCPUs == 0 {
		return 0
	}
	return cpu / sys * float64(f.CPUStats.OnlineCPUs) * 100
}

// Sample summarises one sampling window.
type Sample struct {
	Frames    int     `json:"frames"`
	PeakMiB   float64 `json:"peak_mib"`
	AvgMiB    float64 `json:"avg_mib"`
	CPUAvgPct float64 `json:"cpu_avg_pct"`
	CPUMaxPct float64 `json:"cpu_max_pct"`
}

// Sampler streams stats for one container until Stop.
type Sampler struct {
	mu     sync.Mutex
	frames []statsFrame
	cancel context.CancelFunc
	done   chan struct{}
}

// Start begins streaming stats for container (name or ID).
func (d *Docker) Start(ctx context.Context, container string) (*Sampler, error) {
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+container+"/stats?stream=true", nil)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("docker stats %s: HTTP %d", container, resp.StatusCode)
	}
	s := &Sampler{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var f statsFrame
			if json.Unmarshal(sc.Bytes(), &f) == nil {
				s.mu.Lock()
				s.frames = append(s.frames, f)
				s.mu.Unlock()
			}
		}
	}()
	return s, nil
}

// Stop ends the stream and summarises the frames seen.
func (s *Sampler) Stop() Sample {
	s.cancel()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return Summarize(s.frames)
}

// Summarize computes a Sample from frames; exported for tests.
func Summarize(frames []statsFrame) Sample {
	out := Sample{Frames: len(frames)}
	if len(frames) == 0 {
		return out
	}
	var sumMem, sumCPU float64
	for _, f := range frames {
		m := float64(f.workingSet()) / (1 << 20)
		c := f.cpuPercent()
		sumMem += m
		sumCPU += c
		out.PeakMiB = max(out.PeakMiB, m)
		out.CPUMaxPct = max(out.CPUMaxPct, c)
	}
	out.AvgMiB = sumMem / float64(len(frames))
	out.CPUAvgPct = sumCPU / float64(len(frames))
	return out
}

// Restart restarts container and returns how long it took to report healthy
// (cold startup, doc 16 §5.3), polling its health status every 250 ms up to
// timeout.
func (d *Docker) Restart(ctx context.Context, container string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/containers/"+container+"/restart?t=10", nil)
	resp, err := d.http.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("restart %s: HTTP %d", container, resp.StatusCode)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var info struct {
			State struct {
				Health *struct {
					Status string `json:"Status"`
				} `json:"Health"`
			} `json:"State"`
		}
		r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+container+"/json", nil)
		if resp, err := d.http.Do(r); err == nil {
			json.NewDecoder(resp.Body).Decode(&info)
			resp.Body.Close()
			if info.State.Health != nil && info.State.Health.Status == "healthy" {
				return time.Since(start), nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return 0, fmt.Errorf("%s not healthy within %s", container, timeout)
}

// ImageDigest returns the image ID a container runs, for the report header.
func (d *Docker) ImageDigest(ctx context.Context, container string) string {
	img, _, _ := d.inspect(ctx, container)
	return img
}

// Env returns the container's environment as a map (empty on any error).
func (d *Docker) Env(ctx context.Context, container string) map[string]string {
	_, env, _ := d.inspect(ctx, container)
	out := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// inspect reads a container's image ID and environment.
func (d *Docker) inspect(ctx context.Context, container string) (string, []string, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+container+"/json", nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := d.http.Do(r)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	var info struct {
		Image  string `json:"Image"`
		Config struct {
			Env []string `json:"Env"`
		} `json:"Config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", nil, err
	}
	return info.Image, info.Config.Env, nil
}
