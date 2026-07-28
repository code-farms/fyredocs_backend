package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/promscrape"
	"fyredocs/shared/response"
)

// ServiceStartTime should be set in main.go at startup.
var ServiceStartTime time.Time

// ServerPerformance returns system-level and per-service performance metrics.
func ServerPerformance(c *gin.Context) {
	ctx := c.Request.Context()

	systemInfo := getSystemInfo()
	selfInfo := getSelfServiceInfo()

	// Scrape other services in parallel
	serviceURLs := parseServiceURLs()
	remoteServices := scrapeServicesParallel(ctx, serviceURLs)

	// Merge self into services map
	services := gin.H{
		"analytics-service": selfInfo,
	}
	for name, info := range remoteServices {
		services[name] = info
	}

	// Compute availability
	totalServices := len(services)
	healthyCount := 0
	for _, info := range services {
		if m, ok := info.(gin.H); ok {
			if status, ok := m["status"].(string); ok && status == "healthy" {
				healthyCount++
			}
		}
	}
	unhealthyCount := totalServices - healthyCount
	var uptimePercent float64
	if totalServices > 0 {
		uptimePercent = float64(healthyCount) / float64(totalServices) * 100
	}

	response.OK(c, "Server performance retrieved", gin.H{
		"system":       systemInfo,
		"services":     services,
		"servicesList": buildServicesList(services),
		"availability": gin.H{
			"totalServices":     totalServices,
			"healthyServices":   healthyCount,
			"unhealthyServices": unhealthyCount,
			"uptimePercent":     uptimePercent,
		},
	})
}

// buildServicesList flattens the keyed services map into a table-friendly,
// name-sorted slice for the frontend service table.
func buildServicesList(services gin.H) []gin.H {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]gin.H, 0, len(services))
	for _, name := range names {
		info, ok := services[name].(gin.H)
		if !ok {
			continue
		}
		row := gin.H{
			"name":        name,
			"status":      stringField(info, "status"),
			"uptime":      stringField(info, "uptime"),
			"goVersion":   stringField(info, "goVersion"),
			"goroutines":  info["goroutines"],
			"heapAllocMB": 0.0,
			"heapInuseMB": 0.0,
			"sysMB":       0.0,
		}
		if mem, ok := info["memory"].(gin.H); ok {
			row["heapAllocMB"] = mem["heapAllocMB"]
			row["heapInuseMB"] = mem["heapInuseMB"]
			row["sysMB"] = mem["sysMB"]
		}
		if errMsg, ok := info["error"].(string); ok && errMsg != "" {
			row["error"] = errMsg
		}
		rows = append(rows, row)
	}
	return rows
}

// stringField safely reads a string value from a gin.H map.
func stringField(m gin.H, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// getSystemInfo gathers host-level stats from the Linux /proc filesystem.
//
// LINUX-ONLY: every reader below opens a /proc path. On darwin (local
// development) those reads fail and the fields come back zero or empty rather
// than erroring the request — the dashboard degrades instead of breaking, so a
// developer seeing zeros on a Mac is looking at expected behaviour.
func getSystemInfo() gin.H {
	info := gin.H{
		"cpu": gin.H{
			"count": runtime.NumCPU(),
		},
	}

	// System uptime from /proc/uptime
	if uptimeStr, err := readSystemUptime(); err == nil {
		info["uptime"] = uptimeStr
	}

	// Load averages from /proc/loadavg
	if load1, load5, load15, err := readLoadAvg(); err == nil {
		cpuInfo := info["cpu"].(gin.H)
		cpuInfo["loadAvg1m"] = load1
		cpuInfo["loadAvg5m"] = load5
		cpuInfo["loadAvg15m"] = load15
	}

	// CPU usage from /proc/stat
	if usage, err := readCPUUsage(); err == nil {
		cpuInfo := info["cpu"].(gin.H)
		cpuInfo["usagePercent"] = usage
	}

	// System memory from /proc/meminfo
	if mem, err := readMemInfo(); err == nil {
		info["memory"] = mem
	}

	// Disk usage
	if disk, err := readDiskUsage("/"); err == nil {
		info["storage"] = disk
	}

	return info
}

// getSelfServiceInfo reports this process's own metrics without a network hop,
// since analytics-service cannot usefully scrape itself over HTTP while serving
// the very request doing the scraping.
func getSelfServiceInfo() gin.H {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	uptime := ""
	if !ServiceStartTime.IsZero() {
		uptime = formatDuration(time.Since(ServiceStartTime))
	}

	return gin.H{
		"status":     "healthy",
		"uptime":     uptime,
		"goVersion":  runtime.Version(),
		"goroutines": runtime.NumGoroutine(),
		"memory": gin.H{
			"heapAllocMB":    roundMB(memStats.HeapAlloc),
			"heapInuseMB":    roundMB(memStats.HeapInuse),
			"stackInuseMB":   roundMB(memStats.StackInuse),
			"sysMB":          roundMB(memStats.Sys),
			"numGC":          memStats.NumGC,
			"gcPauseTotalMs": float64(memStats.PauseTotalNs) / 1e6,
		},
	}
}

// scrapeServicesParallel scrapes every service's metrics endpoint concurrently
// and collects the results under a mutex.
//
// Concurrency is what makes this endpoint usable: serially scraping a dozen
// services would sum their latencies, and a single slow service would dominate.
// The map is written from multiple goroutines, hence the mutex — Go maps are not
// safe for concurrent write. Fan-out is bounded by the number of configured
// services, so no semaphore is needed.
func scrapeServicesParallel(ctx context.Context, serviceURLs map[string]string) map[string]gin.H {
	var mu sync.Mutex
	results := make(map[string]gin.H)
	var wg sync.WaitGroup

	for name, url := range serviceURLs {
		wg.Add(1)
		go func(name, url string) {
			defer wg.Done()
			info := scrapeServiceInfo(ctx, name, url)
			mu.Lock()
			results[name] = info
			mu.Unlock()
		}(name, url)
	}

	wg.Wait()
	return results
}

// scrapeServiceInfo probes one service's health and metrics, returning a status
// payload rather than an error so one unreachable service cannot fail the whole
// dashboard.
//
// Raw errors are logged but never returned in the payload: they embed internal
// host:port topology, which must not reach an admin browser response.
func scrapeServiceInfo(ctx context.Context, name, baseURL string) gin.H {
	// Check health first
	healthy, err := promscrape.CheckHealth(ctx, baseURL)
	if err != nil {
		// Raw errors embed internal host:port details — log them, keep the
		// dashboard payload generic.
		slog.Warn("server_performance.health_check", "service", name, "error", err)
		return gin.H{
			"status": "unhealthy",
			"error":  "health check unreachable",
		}
	}
	if !healthy {
		return gin.H{
			"status": "unhealthy",
			"error":  "health check failed",
		}
	}

	// Scrape /metrics for Go runtime stats
	families, err := promscrape.MetricFamilies(ctx, baseURL+"/metrics")
	if err != nil {
		slog.Warn("server_performance.scrape_metrics", "service", name, "error", err)
		return gin.H{
			"status": "healthy",
			"error":  "metrics unavailable",
		}
	}

	goroutines := promscrape.GaugeValue(families, "go_goroutines")
	heapBytes := promscrape.GaugeValue(families, "go_memstats_heap_alloc_bytes")
	heapInuse := promscrape.GaugeValue(families, "go_memstats_heap_inuse_bytes")
	stackInuse := promscrape.GaugeValue(families, "go_memstats_stack_inuse_bytes")
	sysBytes := promscrape.GaugeValue(families, "go_memstats_sys_bytes")

	// Extract uptime from standard process_start_time_seconds metric
	uptime := ""
	if startTimeSec := promscrape.GaugeValue(families, "process_start_time_seconds"); startTimeSec > 0 {
		started := time.Unix(int64(startTimeSec), 0)
		uptime = formatDuration(time.Since(started))
	}

	// Extract Go version from go_info{version="go1.25.8"} metric
	goVersion := promscrape.GaugeLabelValue(families, "go_info", "version")

	return gin.H{
		"status":     "healthy",
		"goroutines": goroutines,
		"uptime":     uptime,
		"goVersion":  goVersion,
		"memory": gin.H{
			"heapAllocMB":  roundMBf(heapBytes),
			"heapInuseMB":  roundMBf(heapInuse),
			"stackInuseMB": roundMBf(stackInuse),
			"sysMB":        roundMBf(sysBytes),
		},
	}
}

// parseServiceURLs builds the name-to-URL scrape map from configuration. These
// are internal mesh addresses, so they belong in logs and never in a response
// body.
func parseServiceURLs() map[string]string {
	raw := strings.TrimSpace(os.Getenv("SERVICE_URLS"))
	if raw == "" {
		// Defaults matching docker-compose service names and ports.
		return map[string]string{
			"api-gateway":      "http://api-gateway:8080",
			"auth-service":     "http://auth-service:8086",
			"job-service":      "http://job-service:8081",
			"convert-from-pdf": "http://convert-from-pdf:8082",
			"convert-to-pdf":   "http://convert-to-pdf:8083",
			"organize-pdf":     "http://organize-pdf:8084",
			"optimize-pdf":     "http://optimize-pdf:8085",
		}
	}

	result := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

// ─── Linux /proc readers ────────────────────────────────────────────────────

// readSystemUptime reads host uptime from /proc/uptime, whose first field is
// seconds since boot. Inside a container this reports the HOST's uptime, not the
// container's, because /proc/uptime is not namespaced.
func readSystemUptime() (string, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "", err
	}
	var uptimeSec float64
	if _, err := fmt.Sscanf(string(data), "%f", &uptimeSec); err != nil {
		return "", err
	}
	return formatDuration(time.Duration(uptimeSec) * time.Second), nil
}

// readLoadAvg reads the 1, 5, and 15-minute load averages from /proc/loadavg.
// Also host-wide rather than container-scoped, and not normalized by core count —
// interpret against the host's CPU count.
func readLoadAvg() (float64, float64, float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	var l1, l5, l15 float64
	if _, err := fmt.Sscanf(string(data), "%f %f %f", &l1, &l5, &l15); err != nil {
		return 0, 0, 0, err
	}
	return l1, l5, l15, nil
}

// readCPUUsage computes CPU utilisation as a percentage.
//
// /proc/stat exposes cumulative jiffy counters since boot, not a rate, so a
// single read cannot yield utilisation — this samples twice 200ms apart and
// takes the delta. That sleep means the function BLOCKS for 200ms, which is why
// it is called once per request from a parallel gather rather than per service.
// A zero delta (a sample too fast to tick) returns 0 rather than dividing by zero.
func readCPUUsage() (float64, error) {
	// Read /proc/stat twice with a small gap to compute CPU usage
	idle1, total1, err := readCPUStat()
	if err != nil {
		return 0, err
	}
	time.Sleep(200 * time.Millisecond)
	idle2, total2, err := readCPUStat()
	if err != nil {
		return 0, err
	}

	totalDelta := total2 - total1
	idleDelta := idle2 - idle1
	if totalDelta == 0 {
		return 0, nil
	}
	return (1.0 - float64(idleDelta)/float64(totalDelta)) * 100, nil
}

// readCPUStat returns the cumulative idle and total jiffy counters from the
// aggregate "cpu " line of /proc/stat. Meaningful only as a delta against a
// second sample; see readCPUUsage.
func readCPUStat() (idle, total uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				return 0, 0, fmt.Errorf("unexpected /proc/stat format")
			}
			var values [10]uint64
			for i := 1; i < len(fields) && i <= 10; i++ {
				fmt.Sscanf(fields[i], "%d", &values[i-1])
			}
			// user, nice, system, idle, iowait, irq, softirq, steal
			for i := 0; i < len(fields)-1 && i < 10; i++ {
				total += values[i]
			}
			idle = values[3] // idle is the 4th field
			return idle, total, nil
		}
	}
	return 0, 0, fmt.Errorf("/proc/stat: cpu line not found")
}

// readMemInfo reads memory totals from /proc/meminfo. Uses MemAvailable rather
// than MemFree, because Linux counts reclaimable page cache as used and MemFree
// alone makes a healthy host look nearly exhausted.
func readMemInfo() (gin.H, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return nil, err
	}

	mem := make(map[string]uint64)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		var val uint64
		fmt.Sscanf(fields[1], "%d", &val)
		mem[key] = val // values are in kB
	}

	totalKB := mem["MemTotal"]
	freeKB := mem["MemFree"]
	availKB := mem["MemAvailable"]
	usedKB := totalKB - availKB

	var usagePercent float64
	if totalKB > 0 {
		usagePercent = float64(usedKB) / float64(totalKB) * 100
	}

	return gin.H{
		"totalMB":      float64(totalKB) / 1024,
		"usedMB":       float64(usedKB) / 1024,
		"freeMB":       float64(freeKB) / 1024,
		"availableMB":  float64(availKB) / 1024,
		"usagePercent": usagePercent,
	}, nil
}

// readDiskUsage reports filesystem usage via statfs. Uses the syscall directly
// rather than shelling out to df, which would mean spawning a process on every
// dashboard request.
func readDiskUsage(path string) (gin.H, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, err
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bavail * uint64(stat.Bsize)
	usedBytes := totalBytes - (stat.Bfree * uint64(stat.Bsize))

	var usagePercent float64
	if totalBytes > 0 {
		usagePercent = float64(usedBytes) / float64(totalBytes) * 100
	}

	return gin.H{
		"totalGB":      float64(totalBytes) / (1024 * 1024 * 1024),
		"usedGB":       float64(usedBytes) / (1024 * 1024 * 1024),
		"freeGB":       float64(freeBytes) / (1024 * 1024 * 1024),
		"usagePercent": usagePercent,
	}, nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// roundMB converts bytes to whole megabytes for display.
func roundMB(bytes uint64) float64 {
	return float64(bytes) / (1024 * 1024)
}

// roundMBf converts bytes to megabytes keeping one decimal, for values where
// whole-MB rounding would lose meaningful precision.
func roundMBf(bytes float64) float64 {
	return bytes / (1024 * 1024)
}

// formatDuration renders a duration in compact human form (for example "3d 4h").
// Go's default Duration string prints hours only, so a multi-day uptime would
// otherwise read as an unhelpfully large hour count.
func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
