package main

import (
	"testing"

	"fyredocs/shared/config"
)

// The in-process cleanup loop (runCleanupLoop) is gated at startup by
// CLEANUP_ENABLED. The sweep itself is covered in internal/cleanup; these
// tests pin the gate's contract: sweeping is ON unless explicitly disabled.
func TestCleanupEnabledGate(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", true},        // unset → default on
		{"true", true},    // explicit on
		{"false", false},  // explicit off (extra replicas / local runs)
		{"0", false},      //
		{"garbage", true}, // unparseable → default on
	}
	for _, tc := range cases {
		t.Setenv("CLEANUP_ENABLED", tc.value)
		if got := config.GetEnvBool("CLEANUP_ENABLED", true); got != tc.want {
			t.Errorf("CLEANUP_ENABLED=%q: got %v, want %v", tc.value, got, tc.want)
		}
	}
}
