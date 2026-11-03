//go:build !race

package imaging

// raceEnabled is false in plain test runs, where timing budgets apply.
const raceEnabled = false
