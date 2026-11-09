//go:build linux

package processing

import (
	"os"
	"path/filepath"
	"strconv"
)

// groupRSS sums the resident memory of every process in process group pgid by
// scanning /proc. Processes that exit mid-scan are skipped. The sum counts
// shared pages once per process, so it overstates a little; it is a recycle
// trigger, not an accounting figure.
func groupRSS(pgid int) int64 {
	if pgid <= 0 {
		return 0
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	page := int64(os.Getpagesize())
	var total int64
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		if g, ok := parseStatPgrp(stat); !ok || g != pgid {
			continue
		}
		statm, err := os.ReadFile(filepath.Join("/proc", e.Name(), "statm"))
		if err != nil {
			continue
		}
		if pages, ok := parseStatmResident(statm); ok {
			total += pages * page
		}
	}
	return total
}
