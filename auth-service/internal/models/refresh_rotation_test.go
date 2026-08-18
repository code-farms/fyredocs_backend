// Tests for refresh-token reuse detection. The invariant under guard: a
// superseded refresh token presented outside the rotation grace window — or any
// token presented against a revoked session — is classified as reuse (theft),
// while a concurrent-tab retry inside the window degrades gracefully and never
// rotates twice.
package models

import (
	"testing"
	"time"
)

func TestClassifyRefresh(t *testing.T) {
	t.Parallel()
	now := time.Now()
	grace := 30 * time.Second
	revoked := now.Add(-time.Minute)
	rotatedInside := now.Add(-10 * time.Second)
	rotatedBoundary := now.Add(-grace)
	rotatedOutside := now.Add(-grace - time.Second)
	rotatedFuture := now.Add(time.Second)

	cases := []struct {
		name        string
		activeMatch bool
		prevMatch   bool
		revokedAt   *time.Time
		rotatedAt   *time.Time
		want        RefreshOutcome
	}{
		{"no match", false, false, nil, nil, OutcomeInvalid},
		{"no match on revoked row", false, false, &revoked, &rotatedInside, OutcomeInvalid},
		{"active match, healthy session", true, false, nil, nil, OutcomeRotate},
		{"active match after earlier rotations", true, false, nil, &rotatedOutside, OutcomeRotate},
		{"active match on revoked session is reuse", true, false, &revoked, &rotatedInside, OutcomeReuse},
		{"prev match inside grace", false, true, nil, &rotatedInside, OutcomeGrace},
		{"prev match exactly at grace boundary", false, true, nil, &rotatedBoundary, OutcomeGrace},
		{"prev match outside grace is reuse", false, true, nil, &rotatedOutside, OutcomeReuse},
		{"prev match with no rotated_at is reuse", false, true, nil, nil, OutcomeReuse},
		{"prev match on revoked session is reuse", false, true, &revoked, &rotatedInside, OutcomeReuse},
		// Clock skew between app instances can put rotated_at ahead of now; a
		// negative elapsed must fall inside grace, not wrap into reuse.
		{"prev match with future rotated_at (clock skew) is grace", false, true, nil, &rotatedFuture, OutcomeGrace},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyRefresh(tc.activeMatch, tc.prevMatch, tc.revokedAt, tc.rotatedAt, now, grace)
			if got != tc.want {
				t.Errorf("ClassifyRefresh(active=%v, prev=%v, revoked=%v, rotated=%v) = %d, want %d",
					tc.activeMatch, tc.prevMatch, tc.revokedAt != nil, tc.rotatedAt, got, tc.want)
			}
		})
	}
}

// TestClassifyRefreshZeroGrace pins AUTH_REFRESH_ROTATE_GRACE=0 as a valid
// hardening configuration: every superseded token is reuse the instant it is
// superseded. Kept as its own table because grace is a fixed parameter of the
// main table above.
func TestClassifyRefreshZeroGrace(t *testing.T) {
	t.Parallel()
	now := time.Now()
	rotatedNow := now
	rotatedJustBefore := now.Add(-time.Nanosecond)

	cases := []struct {
		name        string
		activeMatch bool
		prevMatch   bool
		rotatedAt   *time.Time
		want        RefreshOutcome
	}{
		// elapsed == 0 satisfies <= grace even at zero grace; only the exact
		// rotation instant escapes reuse.
		{"prev match rotated this instant is grace", false, true, &rotatedNow, OutcomeGrace},
		{"prev match rotated any time earlier is reuse", false, true, &rotatedJustBefore, OutcomeReuse},
		{"active match unaffected by zero grace", true, false, nil, OutcomeRotate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyRefresh(tc.activeMatch, tc.prevMatch, nil, tc.rotatedAt, now, 0)
			if got != tc.want {
				t.Errorf("ClassifyRefresh(active=%v, prev=%v, rotated=%v, grace=0) = %d, want %d",
					tc.activeMatch, tc.prevMatch, tc.rotatedAt, got, tc.want)
			}
		})
	}
}
