package models

import (
	"testing"
	"time"
)

func TestClassifyRefresh(t *testing.T) {
	now := time.Now()
	grace := 30 * time.Second
	revoked := now.Add(-time.Minute)
	rotatedInside := now.Add(-10 * time.Second)
	rotatedBoundary := now.Add(-grace)
	rotatedOutside := now.Add(-grace - time.Second)

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
