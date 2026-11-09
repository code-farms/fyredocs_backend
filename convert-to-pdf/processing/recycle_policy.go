package processing

import (
	"bytes"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Recycling a daemon by use. LibreOffice is a long-lived C++ process converting
// untrusted documents; its memory grows and state can leak between conversions,
// and nothing else restarts a daemon that stays up. After each successful
// conversion the daemon is recycled when it has served UNOSERVER_MAX_CONVERSIONS
// documents or its process group's RSS exceeds UNOSERVER_MAX_RSS_MB
// (docs/developer/architecture/next-generation-document-engine/
// 04-libreoffice-replacement-strategy.md §5.2). Both defaults are initial values,
// not measurements; see docs/developer/services/convert-to-pdf.md.

const (
	// defaultMaxConversions is UNOSERVER_MAX_CONVERSIONS when unset.
	defaultMaxConversions = 200
	// defaultMaxRSSMB is UNOSERVER_MAX_RSS_MB when unset: twice the ~300 MB
	// per-daemon budget in deployment/deploy.sh.
	defaultMaxRSSMB = 600
	// recycleReadyTimeout bounds how long a recycled port is held waiting for
	// its replacement daemon. Past it the port is released anyway, and the
	// pre-flight dial in tryUnoconvert routes jobs to the cold path.
	recycleReadyTimeout = 60 * time.Second
)

// envLimit reads a non-negative integer limit where 0 means "disabled".
// Unset, unparsable or negative values give def.
func envLimit(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n >= 0 {
		return n
	}
	return def
}

// maxConversions is UNOSERVER_MAX_CONVERSIONS (0 disables).
func maxConversions() int { return envLimit("UNOSERVER_MAX_CONVERSIONS", defaultMaxConversions) }

// maxRSSMB is UNOSERVER_MAX_RSS_MB (0 disables).
func maxRSSMB() int { return envLimit("UNOSERVER_MAX_RSS_MB", defaultMaxRSSMB) }

// recycleCause returns why a daemon that has served uses conversions should be
// recycled now, or "" to keep it. rssBytes is only called when the RSS limit is
// enabled and the use limit did not already fire, because it scans /proc.
func recycleCause(uses, maxUses int, rssBytes func() int64, maxRSS int) string {
	if maxUses > 0 && uses >= maxUses {
		return "max_conversions"
	}
	if maxRSS > 0 && rssBytes() > int64(maxRSS)<<20 {
		return "max_rss"
	}
	return ""
}

// daemonRSS returns the resident memory of the daemon group on port, or 0 when
// no supervisor runs or the platform cannot tell.
func daemonRSS(port string) int64 {
	s := activeSupervisor.Load()
	if s == nil {
		return 0
	}
	return s.DaemonRSS(port)
}

// recycleThenRelease recycles the daemon on port and returns the port to the
// pool only once the replacement answers (or recycleReadyTimeout passes). The
// caller has already finished its job and must not wait: run it in a goroutine.
// Releasing at once, as the timeout path used to, sent every job routed to the
// port during the restart to the cold LibreOffice fallback; holding the port
// makes those jobs wait for another pooled daemon instead.
func recycleThenRelease(port, cause string) {
	defer pool.release(port)
	pool.resetUses(port)
	s := activeSupervisor.Load()
	if s == nil {
		slog.Warn("cannot recycle unoserver daemon: no supervisor running", "port", port, "cause", cause)
		return
	}
	if !s.RecycleAndWait(port, cause, recycleReadyTimeout) {
		slog.Warn("recycled unoserver daemon not ready in time; releasing its port",
			"port", port, "cause", cause, "waited", recycleReadyTimeout)
	}
}

// parseStatPgrp returns the process-group ID from a /proc/<pid>/stat line. The
// comm field is in parentheses and may itself contain spaces or ')', so fields
// are counted after the LAST ')': state is the first, ppid the second, pgrp the
// third.
func parseStatPgrp(stat []byte) (int, bool) {
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	f := bytes.Fields(stat[i+1:])
	if len(f) < 3 {
		return 0, false
	}
	n, err := strconv.Atoi(string(f[2]))
	return n, err == nil
}

// parseStatmResident returns the resident page count, the second field of
// /proc/<pid>/statm.
func parseStatmResident(statm []byte) (int64, bool) {
	f := bytes.Fields(statm)
	if len(f) < 2 {
		return 0, false
	}
	n, err := strconv.ParseInt(string(f[1]), 10, 64)
	return n, err == nil
}
