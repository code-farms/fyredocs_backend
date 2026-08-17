// Package activity defines the event-type catalog and validation rules for
// the user_activity audit trail.
//
// It lives inside analytics-service rather than shared/ because event types
// are part of this service's data contract, not cross-service business logic
// (Section 1/3): producers elsewhere publish plain strings over HTTP or NATS,
// and this package is the single place that decides what is accepted.
package activity

import (
	"regexp"
	"strings"
)

// Statuses an event may carry. A separate status column is what lets one
// event type (job.started/job.completed in the spec sense) collapse into
// "tool.merge" + status without three near-identical catalog entries.
const (
	StatusStarted   = "started"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// ValidStatus reports whether s is one of the four allowed status values.
func ValidStatus(s string) bool {
	switch s {
	case StatusStarted, StatusSuccess, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// Core event types. Naming is lowercase domain.action to match the existing
// analytics vocabulary (user.login, plan.changed) — the audit trail and
// analytics_events must stay cross-referenceable by name.
//
// Reserved namespaces with no constants yet (features do not exist):
// ai.*, cloud.*, archive.*, subscription.*.
const (
	EventAuthLogin         = "auth.login"
	EventAuthLogout        = "auth.logout"
	EventAuthSignup        = "auth.signup"
	EventAuthPasswordReset = "auth.password_reset"
	EventAuthRefreshReuse  = "auth.refresh_reuse"
	EventAuthProxyLogin    = "auth.proxy_login"

	EventJobStarted   = "job.started"
	EventJobCompleted = "job.completed"
	EventJobFailed    = "job.failed"
	EventJobCancelled = "job.cancelled"

	EventUploadCompleted = "upload.completed"
	EventUploadFailed    = "upload.failed"

	EventShareLinkCreated = "share.link_created"
	EventShareLinkRevoked = "share.link_revoked"

	EventSettingsChanged = "settings.changed"

	EventPlanChanged  = "plan.changed"
	EventPlanLimitHit = "plan.limit_hit"

	EventBackupCompleted = "backup.completed"

	EventAdminActivityExported = "admin.activity_exported"
)

// eventTypePattern is the shape rule: 2-3 lowercase dot-separated segments.
// Validation is pattern + domain allowlist rather than an exact-name set so
// clients can ship a new action within an approved domain (e.g. a new tool)
// without a lockstep backend deploy — but cannot invent whole domains.
var eventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_-]+){1,2}$`)

// allowedDomains is the closed set of first segments the ingest path accepts.
// Growing this list is a deliberate act; a typo'd or hostile domain is
// rejected per-event, never the whole batch.
var allowedDomains = map[string]bool{
	"auth":       true,
	"job":        true,
	"upload":     true,
	"document":   true,
	"annotation": true,
	"signature":  true,
	"scan":       true,
	"ocr":        true,
	"share":      true,
	"settings":   true,
	"session":    true,
	"backup":     true,
	"plan":       true,
	"admin":      true,
}

// ValidEventType reports whether t is a well-formed event type in an
// approved domain.
func ValidEventType(t string) bool {
	if !eventTypePattern.MatchString(t) {
		return false
	}
	domain, _, _ := strings.Cut(t, ".")
	return allowedDomains[domain]
}

// sensitiveMetadataKey matches metadata keys that must never be persisted,
// whatever the client sends. This is defense in depth behind the client SDKs'
// whitelists — the server cannot trust that every future client builds
// metadata correctly.
var sensitiveMetadataKey = regexp.MustCompile(`(?i)password|token|secret|content|text|prompt|authorization|cookie|key`)

// SensitiveMetadataKey reports whether a metadata key must be stripped at
// ingest.
func SensitiveMetadataKey(k string) bool {
	return sensitiveMetadataKey.MatchString(k)
}
