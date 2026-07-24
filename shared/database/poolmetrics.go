package database

import (
	"database/sql"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Pool-metrics: exposes the service's sql.DB connection-pool stats on the
// default Prometheus registry so every DB-using service's /metrics carries
// them (no per-service wiring). Read at scrape time via a custom collector so
// the values never go stale. No `service` label — Prometheus already tags each
// scrape target with instance/job.
var (
	poolOnce    sync.Once
	poolStatsMu sync.RWMutex
	poolStatsFn func() sql.DBStats
)

var (
	descPoolOpen = prometheus.NewDesc("db_pool_open_connections",
		"Open connections to the database (in use + idle).", nil, nil)
	descPoolInUse = prometheus.NewDesc("db_pool_in_use_connections",
		"Connections currently in use.", nil, nil)
	descPoolMaxOpen = prometheus.NewDesc("db_pool_max_open_connections",
		"Configured maximum number of open connections.", nil, nil)
	descPoolWaitCount = prometheus.NewDesc("db_pool_wait_count_total",
		"Cumulative number of connection waits (pool exhaustion pressure).", nil, nil)
)

// poolCollector reads pool stats at collection (scrape) time.
type poolCollector struct{ stats func() sql.DBStats }

func (c poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descPoolOpen
	ch <- descPoolInUse
	ch <- descPoolMaxOpen
	ch <- descPoolWaitCount
}

func (c poolCollector) Collect(ch chan<- prometheus.Metric) {
	if c.stats == nil {
		return
	}
	s := c.stats()
	ch <- prometheus.MustNewConstMetric(descPoolOpen, prometheus.GaugeValue, float64(s.OpenConnections))
	ch <- prometheus.MustNewConstMetric(descPoolInUse, prometheus.GaugeValue, float64(s.InUse))
	ch <- prometheus.MustNewConstMetric(descPoolMaxOpen, prometheus.GaugeValue, float64(s.MaxOpenConnections))
	ch <- prometheus.MustNewConstMetric(descPoolWaitCount, prometheus.CounterValue, float64(s.WaitCount))
}

// registerPoolMetrics points the pool collector at sqlDB and registers it on
// the default registry exactly once. Safe to call on every ConnectFromEnv:
// later calls just repoint at the newest *sql.DB (a service has one pool), so a
// reconnect (or test) never double-registers and never reads a stale handle.
func registerPoolMetrics(sqlDB *sql.DB) {
	poolStatsMu.Lock()
	poolStatsFn = sqlDB.Stats
	poolStatsMu.Unlock()

	poolOnce.Do(func() {
		prometheus.MustRegister(poolCollector{stats: func() sql.DBStats {
			poolStatsMu.RLock()
			fn := poolStatsFn
			poolStatsMu.RUnlock()
			if fn == nil {
				return sql.DBStats{}
			}
			return fn()
		}})
	})
}
