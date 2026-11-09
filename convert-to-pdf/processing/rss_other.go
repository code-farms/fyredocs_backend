//go:build !linux

package processing

// groupRSS is unavailable without /proc; it reports 0, so the RSS recycle
// limit never fires. The worker only runs in Linux containers.
func groupRSS(pgid int) int64 { return 0 }
