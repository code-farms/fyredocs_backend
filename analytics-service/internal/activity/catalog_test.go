package activity

import "testing"

func TestValidStatus(t *testing.T) {
	for _, s := range []string{StatusStarted, StatusSuccess, StatusFailed, StatusCancelled} {
		if !ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "SUCCESS", "done", "pending", "started "} {
		if ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = true, want false", s)
		}
	}
}

func TestValidEventType_AcceptsCatalogAndDomainExtensions(t *testing.T) {
	valid := []string{
		EventAuthLogin,
		EventJobCompleted,
		EventShareLinkCreated,
		EventAdminActivityExported,
		// New actions inside approved domains must pass without a backend
		// change — that is the point of pattern+allowlist over an exact set.
		"job.merge-pdf",
		"document.pinned",
		"scan.filter_applied",
		"settings.theme.changed",
	}
	for _, v := range valid {
		if !ValidEventType(v) {
			t.Errorf("ValidEventType(%q) = false, want true", v)
		}
	}
}

func TestValidEventType_RejectsMalformedAndUnknownDomains(t *testing.T) {
	invalid := []string{
		"",
		"login",                  // no domain separator
		"AUTH.LOGIN",             // wrong case
		"auth.",                  // empty action
		".login",                 // empty domain
		"ai.summary_completed",   // reserved namespace, not yet approved
		"cloud.upload_completed", // reserved namespace
		"evil.injection",         // unknown domain
		"auth.login.extra.deep",  // too many segments
		"auth.login;drop table",  // injection characters
		"auth login",             // whitespace
	}
	for _, v := range invalid {
		if ValidEventType(v) {
			t.Errorf("ValidEventType(%q) = true, want false", v)
		}
	}
}

func TestSensitiveMetadataKey(t *testing.T) {
	sensitive := []string{
		"password", "newPassword", "accessToken", "TOKEN", "clientSecret",
		"documentContent", "ocrText", "aiPrompt", "authorization", "cookie",
		"apiKey", "privateKey",
	}
	for _, k := range sensitive {
		if !SensitiveMetadataKey(k) {
			t.Errorf("SensitiveMetadataKey(%q) = false, want true", k)
		}
	}
	safe := []string{"fileSize", "durationMs", "pageCount", "toolId", "oldPlan", "newPlan", "language"}
	for _, k := range safe {
		if SensitiveMetadataKey(k) {
			t.Errorf("SensitiveMetadataKey(%q) = true, want false", k)
		}
	}
}
