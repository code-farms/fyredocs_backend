//go:build race

package imaging

// raceEnabled: the race detector slows CPU-bound pixel loops 10–20×, so
// timing budgets are only asserted without it.
const raceEnabled = true
