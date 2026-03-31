package handlers

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"analytics-service/internal/promscrape"
	"esydocs/shared/response"
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
		"system":   systemInfo,
		"services": services,
		"availability": gin.H{
			"totalServices":     totalServices,
			"healthyServices":   healthyCount,
			"unhealthyServices": unhealthyCount,
			"uptimePercent":     uptimePercent,
		},
	})
}

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

func getSelfServiceInfo() gin.H {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	uptime := ""
	if !ServiceStartTime.IsZero() {
		uptime = formatDuration(time.Since(ServiceStartTime))
	}

	return gin.H{
		"status":    "healthy",
		"uptime":    uptime,
		"goVersion": runtime.Version(),
		"goroutines": runtime.NumGoroutine(),
		"memory": gin.H{
			"heapAllocMB":   roundMB(memStats.HeapAlloc),
			"heapInuseMB":   roundMB(memStats.HeapInuse),
			"stackInuseMB":  roundMB(memStats.StackInuse),
			"sysMB":         roundMB(memStats.Sys),
			"numGC":         memStats.NumGC,
			"gcPauseTotalMs": float64(memStats.PauseTotalNs) / 1e6,
		},
	}
}

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

func scrapeServiceInfo(ctx context.Context, name, baseURL string) gin.H {
	// Check health first
	healthy, err := promscrape.CheckHealth(ctx, baseURL)
	if err != nil {
		return gin.H{
			"status": "unhealthy",
			"error":  err.Error(),
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
		return gin.H{
			"status": "healthy",
			"error":  "metrics unavailable: " + err.Error(),
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
			"cleanup-worker":   "http://cleanup-worker:8088",
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

// --- Linux /proc readers ---

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

// --- Helpers ---

func roundMB(bytes uint64) float64 {
	return float64(bytes) / (1024 * 1024)
}

func roundMBf(bytes float64) float64 {
	return bytes / (1024 * 1024)
}

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
