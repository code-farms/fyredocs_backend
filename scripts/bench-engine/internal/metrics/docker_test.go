package metrics

import (
	"math"
	"testing"
)

func frame(usage, inactive, cpu, precpu, sys, presys uint64) statsFrame {
	var f statsFrame
	f.MemoryStats.Usage = usage
	f.MemoryStats.Stats = map[string]uint64{"inactive_file": inactive}
	f.CPUStats.CPUUsage.TotalUsage = cpu
	f.PreCPUStats.CPUUsage.TotalUsage = precpu
	f.CPUStats.SystemUsage = sys
	f.PreCPUStats.SystemUsage = presys
	f.CPUStats.OnlineCPUs = 4
	return f
}

func TestSummarize(t *testing.T) {
	const mib = 1 << 20
	s := Summarize([]statsFrame{
		frame(300*mib, 100*mib, 200, 100, 1000, 800),  // 200 MiB, 0.5 of 4 CPUs => 200%
		frame(500*mib, 100*mib, 300, 200, 1400, 1000), // 400 MiB, 100/400*4*100 = 100%
	})
	if s.Frames != 2 || s.PeakMiB != 400 || s.AvgMiB != 300 {
		t.Errorf("memory: %+v", s)
	}
	if math.Abs(s.CPUMaxPct-200) > 1e-9 || math.Abs(s.CPUAvgPct-150) > 1e-9 {
		t.Errorf("cpu: %+v", s)
	}
	if z := Summarize(nil); z.Frames != 0 || z.PeakMiB != 0 {
		t.Errorf("empty: %+v", z)
	}
}

func TestWorkingSetNeverUnderflows(t *testing.T) {
	if ws := frame(10, 20, 0, 0, 0, 0).workingSet(); ws != 10 {
		t.Errorf("inactive > usage: %d, want usage", ws)
	}
}
