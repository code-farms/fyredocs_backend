package response

// Canonical, machine-readable error codes returned in the envelope's
// `error.code`. Clients switch on these, so they are a stable API contract —
// prefer an existing constant over inventing a new literal, and keep the string
// values stable. Grouped by concern.
//
// Auth codes intentionally match shared/authverify (AUTH_UNAUTHORIZED /
// AUTH_FORBIDDEN) so middleware and handlers speak one vocabulary.
const (
	// Client / validation (400).
	CodeInvalidInput = "INVALID_INPUT" // malformed or invalid request body/params (canonical for the old INVALID_BODY / INVALID_REQUEST)
	CodeInvalidID    = "INVALID_ID"    // a path/query identifier failed to parse

	// Auth (401 / 403).
	CodeUnauthorized = "AUTH_UNAUTHORIZED"  // missing/invalid credentials
	CodeTokenExpired = "AUTH_TOKEN_EXPIRED" // valid-but-expired token → client may silently refresh
	CodeForbidden    = "AUTH_FORBIDDEN"     // authenticated but not allowed

	// Resource (404 / 409).
	CodeNotFound = "NOT_FOUND"
	CodeConflict = "CONFLICT"

	// Quota / limits (413 / 429).
	CodeFileTooLarge = "FILE_TOO_LARGE"
	CodeTooManyFiles = "TOO_MANY_FILES"
	CodeRateLimited  = "RATE_LIMIT_EXCEEDED"

	// Server / dependency (5xx).
	CodeServerError         = "SERVER_ERROR"         // unexpected internal failure (incl. recovered panics)
	CodeUpstreamUnavailable = "UPSTREAM_UNAVAILABLE" // a downstream service could not be reached (502)
	CodeServiceUnavailable  = "SERVICE_UNAVAILABLE"  // a required dependency is down (503)

	// AI (ai-service). Distinct from the generic codes so the mobile app's
	// jobErrors table can say something specific instead of a generic toast.
	CodeAIUnavailable     = "AI_UNAVAILABLE"       // model provider unconfigured, down, or breaker open (503)
	CodeAIQuotaExceeded   = "AI_QUOTA_EXCEEDED"    // the caller's AI token quota for the period is used (429)
	CodeAIRequestTooLarge = "AI_REQUEST_TOO_LARGE" // input over the service's limits or the model's context (413)
	CodeAIDocumentExpired = "AI_DOCUMENT_EXPIRED"  // a docRef is no longer cached — resend the pages (410)
)
