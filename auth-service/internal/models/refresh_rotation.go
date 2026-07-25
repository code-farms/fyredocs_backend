package models

import "time"

// RefreshOutcome classifies a refresh attempt against the matched session row.
type RefreshOutcome int

const (
	// OutcomeInvalid — no session matched, or the match is unusable: reject 401.
	OutcomeInvalid RefreshOutcome = iota
	// OutcomeRotate — the presented token is the session's current refresh
	// token: rotate the pair and issue new access + refresh tokens.
	OutcomeRotate
	// OutcomeGrace — the presented token was superseded by a rotation within the
	// grace window (a concurrent refresh from another tab/request lost the
	// race): issue a fresh access token but do NOT rotate again.
	OutcomeGrace
	// OutcomeReuse — the presented token was superseded outside the grace
	// window, or the session is already revoked: token reuse. Revoke the
	// session family and reject.
	OutcomeReuse
)

// ClassifyRefresh decides what a refresh attempt means given how the presented
// token matched the session row. activeMatch is true when the token equals the
// session's current refresh hash; prevMatch when it equals the superseded hash.
// The two are mutually exclusive by construction (the caller looks up by one,
// then the other). Pure function so the full decision table is unit-testable
// without a database.
func ClassifyRefresh(activeMatch, prevMatch bool, revokedAt, rotatedAt *time.Time, now time.Time, grace time.Duration) RefreshOutcome {
	switch {
	case !activeMatch && !prevMatch:
		return OutcomeInvalid
	case revokedAt != nil:
		// Any replay against a revoked session — by either hash — is reuse.
		return OutcomeReuse
	case activeMatch:
		return OutcomeRotate
	case rotatedAt != nil && now.Sub(*rotatedAt) <= grace:
		return OutcomeGrace
	default:
		return OutcomeReuse
	}
}
