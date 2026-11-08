//go:build integration

package handlers

import (
	"net/http"
	"testing"

	"auth-service/internal/models"
)

// seedPlanRows inserts the catalogue rows these tests resolve against.
func seedPlanRows(t *testing.T, plans ...models.SubscriptionPlan) {
	t.Helper()
	for _, p := range plans {
		p := p
		if err := models.DB.Create(&p).Error; err != nil {
			t.Fatalf("seed plan %s: %v", p.Name, err)
		}
	}
	resetPlanCache(t)
}

// planLimitsOf extracts data.user.planLimits from an auth response envelope.
func planLimitsOf(t *testing.T, env authEnvelope) map[string]any {
	t.Helper()
	user, _ := env.Data["user"].(map[string]any)
	limits, _ := user["planLimits"].(map[string]any)
	if limits == nil {
		t.Fatalf("data.user.planLimits missing: %v", env.Data["user"])
	}
	return limits
}

// TestPlanLimits_DB_LoginMeRefresh pins that every response a client reads
// its limits from carries the user's subscription_plans row verbatim, so a
// DB edit is what the web and mobile validators enforce.
func TestPlanLimits_DB_LoginMeRefresh(t *testing.T) {
	withDB(t)
	seedPlanRows(t, testFreePlan, testProPlan)
	r := newAuthRouter(t)
	const password = "limits-pass-123"
	signupUser(t, r, "kim@example.com", password)
	user := loadOnlyUser(t)
	if err := models.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("plan_name", "pro").Error; err != nil {
		t.Fatalf("assign pro: %v", err)
	}

	assertPro := func(where string, limits map[string]any) {
		t.Helper()
		if limits["name"] != "pro" || limits["maxFileSizeMb"] != float64(300) ||
			limits["maxFilesPerJob"] != float64(50) || limits["maxJobInputMb"] != float64(300) {
			t.Errorf("%s planLimits = %v, want the pro row 300/50/300", where, limits)
		}
	}

	login, env := doAuth(t, r, http.MethodPost, "/auth/login", map[string]any{
		"email": "kim@example.com", "password": password,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d (body %s)", login.Code, login.Body.String())
	}
	assertPro("login", planLimitsOf(t, env))

	me, env := doAuth(t, r, http.MethodGet, "/auth/me", nil, accessCookie(cookieValue(t, login, "access_token")), nil)
	if me.Code != http.StatusOK {
		t.Fatalf("me status = %d (body %s)", me.Code, me.Body.String())
	}
	assertPro("me", planLimitsOf(t, env))

	ref, env := doAuth(t, r, http.MethodPost, "/auth/refresh", nil, refreshCookie(cookieValue(t, login, "refresh_token")), nil)
	if ref.Code != http.StatusOK {
		t.Fatalf("refresh status = %d (body %s)", ref.Code, ref.Body.String())
	}
	assertPro("refresh", planLimitsOf(t, env))

	// An edit to the row reaches the next login once the plan cache is cold.
	if err := models.DB.Model(&models.SubscriptionPlan{}).Where("name = ?", "pro").Update("max_file_size_mb", 250).Error; err != nil {
		t.Fatalf("edit pro row: %v", err)
	}
	resetPlanCache(t)
	_, env = doAuth(t, r, http.MethodPost, "/auth/login", map[string]any{
		"email": "kim@example.com", "password": password,
	}, nil, nil)
	if got := planLimitsOf(t, env)["maxFileSizeMb"]; got != float64(250) {
		t.Errorf("after DB edit maxFileSizeMb = %v, want 250", got)
	}
}

// TestGetAllPlans_DB_IncludesJobTotal pins that /auth/plans exposes
// maxJobInputMb, which guest clients use as their per-job total.
func TestGetAllPlans_DB_IncludesJobTotal(t *testing.T) {
	withDB(t)
	seedPlanRows(t, testGuestPlan, testFreePlan, testProPlan)
	r := newAuthRouter(t)
	r.GET("/auth/plans", GetAllPlans)

	rec, env := doAuth(t, r, http.MethodGet, "/auth/plans", nil, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	plans, _ := env.Data["plans"].([]any)
	want := map[string]float64{"guest": 100, "free": 300, "pro": 300}
	if len(plans) != len(want) {
		t.Fatalf("plans = %v, want %d rows", plans, len(want))
	}
	for _, p := range plans {
		row, _ := p.(map[string]any)
		name, _ := row["name"].(string)
		if row["maxJobInputMb"] != want[name] {
			t.Errorf("%s maxJobInputMb = %v, want %v", name, row["maxJobInputMb"], want[name])
		}
	}
}
