package database

import (
	"database/sql"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestPoolCollector verifies the collector emits sql.DB.Stats() values at
// collection time (scrape-time read, not a stale snapshot). Uses a private
// registry + injected fake stats so no real database is required.
func TestPoolCollector(t *testing.T) {
	fake := sql.DBStats{OpenConnections: 7, InUse: 5, MaxOpenConnections: 50, WaitCount: 3}
	c := poolCollector{stats: func() sql.DBStats { return fake }}

	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	got := map[string]float64{}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			got[mf.GetName()] = metricValue(m)
		}
	}

	want := map[string]float64{
		"db_pool_open_connections":     7,
		"db_pool_in_use_connections":   5,
		"db_pool_max_open_connections": 50,
		"db_pool_wait_count_total":     3,
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %v, want %v (all: %v)", name, got[name], w, got)
		}
	}
}

// TestPoolCollectorScrapeTimeRead confirms the value tracks live changes.
func TestPoolCollectorScrapeTimeRead(t *testing.T) {
	inUse := 1
	c := poolCollector{stats: func() sql.DBStats { return sql.DBStats{InUse: inUse} }}
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	read := func() float64 {
		mfs, _ := reg.Gather()
		for _, mf := range mfs {
			if mf.GetName() == "db_pool_in_use_connections" {
				return metricValue(mf.GetMetric()[0])
			}
		}
		return -1
	}
	if v := read(); v != 1 {
		t.Fatalf("initial in_use=%v want 1", v)
	}
	inUse = 9
	if v := read(); v != 9 {
		t.Errorf("after change in_use=%v want 9 (scrape-time read broken)", v)
	}
}

func metricValue(m *dto.Metric) float64 {
	if g := m.GetGauge(); g != nil {
		return g.GetValue()
	}
	if c := m.GetCounter(); c != nil {
		return c.GetValue()
	}
	return -1
}
