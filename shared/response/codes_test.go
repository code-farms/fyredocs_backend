package response

import "testing"

// TestErrorCodeValuesAreStable pins the wire values of the canonical error
// codes. Clients switch on these strings; changing one is an API contract
// break and must be deliberate — if this test fails, you are renaming a code
// every client sees.
func TestErrorCodeValuesAreStable(t *testing.T) {
	t.Parallel()
	pins := map[string]string{
		CodeInvalidInput:        "INVALID_INPUT",
		CodeInvalidID:           "INVALID_ID",
		CodeUnauthorized:        "AUTH_UNAUTHORIZED",
		CodeTokenExpired:        "AUTH_TOKEN_EXPIRED",
		CodeForbidden:           "AUTH_FORBIDDEN",
		CodeNotFound:            "NOT_FOUND",
		CodeConflict:            "CONFLICT",
		CodeFileTooLarge:        "FILE_TOO_LARGE",
		CodeTooManyFiles:        "TOO_MANY_FILES",
		CodeRateLimited:         "RATE_LIMIT_EXCEEDED",
		CodeServerError:         "SERVER_ERROR",
		CodeUpstreamUnavailable: "UPSTREAM_UNAVAILABLE",
		CodeServiceUnavailable:  "SERVICE_UNAVAILABLE",
	}
	for got, want := range pins {
		if got != want {
			t.Errorf("canonical error code changed: got %q, want %q", got, want)
		}
	}
}
