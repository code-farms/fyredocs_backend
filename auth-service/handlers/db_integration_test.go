//go:build integration

// Integration tests for the identity lifecycle over real Postgres rows:
// signup persisting a bcrypt hash (never plaintext), email-normalized
// dedupe, the uniform login 401 (the timing-equalizer path now actually
// executes against a real users table), refresh rotation / grace / reuse
// detection against committed user_sessions rows, logout revocation, and the
// internal plan/revocation API. Handlers commit and re-read across requests,
// so every test uses Truncate isolation — never dbtest.Tx (see
// docs/developer/architecture/testing-strategy.md).
package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"auth-service/internal/email"
	"auth-service/internal/migrations"
	"auth-service/internal/models"
	"auth-service/internal/token"
	"fyredocs/shared/authverify"
	"fyredocs/shared/database/dbtest"
)

// withDB applies this service's migrations to the shared test database and
// swaps the package-global handle for the test's duration. The service name
// must stay "auth-service" — it selects the goose history table production
// writes (models.Migrate). It also drains the package-global plan cache:
// entries cached by an earlier test would otherwise survive the table
// truncation and leak stale plan rows into this one.
func withDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := dbtest.Open(t, "auth-service", migrations.FS)
	prev := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = prev })
	dbtest.Truncate(t, db, "users", "user_sessions", "subscription_plans")
	resetPlanCache(t)
	return db
}

// resetPlanCache empties the in-process plan cache now and on cleanup, so
// plan rows this test seeds (or relies on being absent) are actually read
// from the database.
func resetPlanCache(t testing.TB) {
	t.Helper()
	drain := func() {
		planCacheMu.Lock()
		planCache = map[string]cachedPlan{}
		planCacheMu.Unlock()
	}
	drain()
	t.Cleanup(drain)
}

// testInternalToken guards the /internal group in the test router, standing
// in for INTERNAL_API_TOKEN.
const testInternalToken = "internal-integration-secret"

// newAuthRouter builds the auth routes the way routes.SetupRouter wires them
// (built by hand — importing auth-service/routes here would be an import
// cycle), with a real HS256 issuer/verifier pair from env. Denylist and
// RedisClient stay nil: every handler guards nil for both (denylist and plan
// cache become no-ops), which keeps these tests on the Postgres contract.
func newAuthRouter(t *testing.T) *gin.Engine {
	t.Helper()
	t.Setenv("JWT_HS256_SECRET", strings.Repeat("a1b2c3d4", 8))
	t.Setenv("JWT_ISSUER", "fyredocs-auth-test")
	t.Setenv("JWT_AUDIENCE", "fyredocs-test-clients")
	t.Setenv("INTERNAL_API_TOKEN", testInternalToken)

	issuer, err := token.NewIssuerFromEnv()
	if err != nil {
		t.Fatalf("NewIssuerFromEnv: %v", err)
	}
	verifier, err := authverify.NewVerifierFromEnv(nil)
	if err != nil {
		t.Fatalf("NewVerifierFromEnv: %v", err)
	}
	authMW := authverify.GinAuthMiddleware(authverify.GinMiddlewareOptions{Verifier: verifier})

	ae := &AuthEndpoints{Issuer: issuer, Mailer: email.NoopMailer{}}

	r := gin.New()
	r.POST("/auth/signup", ae.Signup)
	r.POST("/auth/login", ae.Login)
	r.POST("/auth/refresh", ae.Refresh)
	protected := r.Group("/auth", authMW)
	protected.GET("/me", ae.Me)
	protected.POST("/logout", ae.Logout)
	internal := r.Group("/internal", authverify.InternalTokenGin(testInternalToken))
	internal.GET("/users/:id/plan", GetUserPlan)
	internal.POST("/users/:id/revoke-sessions", ae.RevokeUserSessions)
	return r
}

// authEnvelope mirrors the standard {success,message,data,error} response
// envelope used by every handler.
type authEnvelope struct {
	Success bool           `json:"success"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Details string `json:"details"`
	} `json:"error"`
}

// doAuth issues one request, attaching any cookies (the browser's replay of
// Set-Cookie) and headers, and decodes the standard envelope.
func doAuth(t *testing.T, r *gin.Engine, method, path string, body any, cookies []*http.Cookie, headers map[string]string) (*httptest.ResponseRecorder, authEnvelope) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var env authEnvelope
	if rec.Body.Len() > 0 && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope (%d %s): %v", rec.Code, rec.Body.String(), err)
		}
	}
	return rec, env
}

// signupUser registers an account through the real signup handler and fails
// the test on anything but success.
func signupUser(t *testing.T, r *gin.Engine, emailAddr, password string) *httptest.ResponseRecorder {
	t.Helper()
	rec, _ := doAuth(t, r, http.MethodPost, "/auth/signup", map[string]any{
		"email":    emailAddr,
		"password": password,
		"fullName": "Integration Tester",
		"country":  "IN",
	}, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("signup: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	return rec
}

// cookieValue extracts a non-cleared Set-Cookie value from a response.
func cookieValue(t *testing.T, rec *httptest.ResponseRecorder, name string) string {
	t.Helper()
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == name && ck.Value != "" && ck.MaxAge >= 0 {
			return ck.Value
		}
	}
	t.Fatalf("cookie %q not set (headers %v)", name, rec.Header().Values("Set-Cookie"))
	return ""
}

// hasClearedCookie reports whether the response expires the named cookie.
func hasClearedCookie(rec *httptest.ResponseRecorder, name string) bool {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == name && ck.Value == "" && ck.MaxAge < 0 {
			return true
		}
	}
	return false
}

// refreshCookie wraps a raw refresh token as the cookie /auth/refresh reads.
func refreshCookie(value string) []*http.Cookie {
	return []*http.Cookie{{Name: "refresh_token", Value: value}}
}

// accessCookie wraps a raw access token as the cookie the auth middleware and
// logout read.
func accessCookie(value string) []*http.Cookie {
	return []*http.Cookie{{Name: "access_token", Value: value}}
}

// loadOnlyUser returns the single users row, failing if there is not exactly one.
func loadOnlyUser(t *testing.T) models.User {
	t.Helper()
	var users []models.User
	if err := models.DB.Find(&users).Error; err != nil {
		t.Fatalf("load users: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("users rows = %d, want 1", len(users))
	}
	return users[0]
}

// sessionByRefresh resolves the committed session row for a raw refresh token.
func sessionByRefresh(t *testing.T, raw string) models.UserSession {
	t.Helper()
	var s models.UserSession
	if err := models.DB.Where("refresh_token_hash = ?", models.HashToken(raw)).First(&s).Error; err != nil {
		t.Fatalf("load session by refresh hash: %v", err)
	}
	return s
}

func countSessions(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := models.DB.Model(&models.UserSession{}).Count(&n).Error; err != nil {
		t.Fatalf("count user_sessions: %v", err)
	}
	return n
}

func TestSignup_DB_PersistsBcryptHashAndSession(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	const password = "correct-horse-battery"

	rec := signupUser(t, r, "alice@example.com", password)

	user := loadOnlyUser(t)
	if user.PasswordHash == password || !strings.HasPrefix(user.PasswordHash, "$2") {
		t.Errorf("stored password hash %q is not a bcrypt hash", user.PasswordHash)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		t.Errorf("stored hash does not verify the signup password: %v", err)
	}
	// Neither the raw password nor the hash may reach the response body.
	if strings.Contains(rec.Body.String(), password) || strings.Contains(rec.Body.String(), user.PasswordHash) {
		t.Error("signup response leaks credential material")
	}

	// One login = one session row, storing HASHES of both issued tokens.
	if n := countSessions(t); n != 1 {
		t.Fatalf("user_sessions rows = %d, want 1", n)
	}
	access := cookieValue(t, rec, "access_token")
	refresh := cookieValue(t, rec, "refresh_token")
	session := sessionByRefresh(t, refresh)
	if session.UserID != user.ID {
		t.Errorf("session UserID = %s, want %s", session.UserID, user.ID)
	}
	if session.AccessTokenHash != models.HashToken(access) {
		t.Error("session access hash does not match the issued access token")
	}
	if session.RefreshTokenHash == refresh || session.AccessTokenHash == access {
		t.Error("session stores a raw token instead of its hash")
	}
}

func TestSignup_DB_DuplicateEmailConflict(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	signupUser(t, r, "dup@example.com", "password-one")

	rec, env := doAuth(t, r, http.MethodPost, "/auth/signup", map[string]any{
		"email":    "dup@example.com",
		"password": "password-two",
		"fullName": "Second Try",
		"country":  "IN",
	}, nil, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if env.Error == nil || env.Error.Code != "USER_ALREADY_EXISTS" {
		t.Errorf("error = %+v, want USER_ALREADY_EXISTS", env.Error)
	}
	loadOnlyUser(t)
}

// TestSignup_DB_EmailNormalizationDedupes pins that email is an identity key
// only as good as its normalization: a case variant must hit the same row on
// both the write (dedupe) and read (login) paths. (Whitespace padding never
// reaches normalizeEmail — the binding's `email` validator rejects it first.)
func TestSignup_DB_EmailNormalizationDedupes(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	const password = "normalized-pass-1"
	signupUser(t, r, "Case@Example.COM", password)

	if got := loadOnlyUser(t).Email; got != "case@example.com" {
		t.Errorf("stored email = %q, want the normalized case@example.com", got)
	}

	rec, env := doAuth(t, r, http.MethodPost, "/auth/signup", map[string]any{
		"email":    "case@example.com",
		"password": "another-pass-2",
		"fullName": "Case Variant",
		"country":  "IN",
	}, nil, nil)
	if rec.Code != http.StatusConflict || env.Error == nil || env.Error.Code != "USER_ALREADY_EXISTS" {
		t.Errorf("variant signup: status/error = %d/%+v, want 409 USER_ALREADY_EXISTS", rec.Code, env.Error)
	}

	// The normalization also holds on the read path: a shouty login finds
	// the same account.
	rec, _ = doAuth(t, r, http.MethodPost, "/auth/login", map[string]any{
		"email": "CASE@EXAMPLE.COM", "password": password,
	}, nil, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("case-variant login: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestLogin_DB_SuccessCreatesHashedSession(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	const password = "login-pass-123"
	signupUser(t, r, "bob@example.com", password)

	rec, env := doAuth(t, r, http.MethodPost, "/auth/login", map[string]any{
		"email": "bob@example.com", "password": password,
	}, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if userData, _ := env.Data["user"].(map[string]any); userData == nil || userData["email"] != "bob@example.com" {
		t.Errorf("data.user = %v, want the account profile", env.Data["user"])
	}

	// Signup created session 1; this login must add session 2 with hashes.
	if n := countSessions(t); n != 2 {
		t.Fatalf("user_sessions rows = %d, want 2 (signup + login)", n)
	}
	refresh := cookieValue(t, rec, "refresh_token")
	session := sessionByRefresh(t, refresh)
	if session.RefreshTokenHash != models.HashToken(refresh) || session.RefreshTokenHash == refresh {
		t.Error("login session must store the refresh token's SHA-256 hash, never the raw token")
	}
}

// TestLogin_DB_UniformUnauthorized pins the account-enumeration defense over
// a real users table: a wrong password for a real account and an unknown
// email (the dummy-bcrypt timing-equalizer path) must be indistinguishable in
// status, code, and message.
func TestLogin_DB_UniformUnauthorized(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	signupUser(t, r, "carol@example.com", "real-password-1")

	wrongPass, wrongEnv := doAuth(t, r, http.MethodPost, "/auth/login", map[string]any{
		"email": "carol@example.com", "password": "not-the-password",
	}, nil, nil)
	unknownEmail, unknownEnv := doAuth(t, r, http.MethodPost, "/auth/login", map[string]any{
		"email": "nobody@example.com", "password": "not-the-password",
	}, nil, nil)

	for name, rec := range map[string]*httptest.ResponseRecorder{"wrong password": wrongPass, "unknown email": unknownEmail} {
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
	}
	if wrongEnv.Error == nil || unknownEnv.Error == nil ||
		wrongEnv.Error.Code != "INVALID_CREDENTIALS" || unknownEnv.Error.Code != wrongEnv.Error.Code ||
		unknownEnv.Message != wrongEnv.Message {
		t.Errorf("the two failure shapes differ (oracle): %+v %q vs %+v %q",
			wrongEnv.Error, wrongEnv.Message, unknownEnv.Error, unknownEnv.Message)
	}
	if n := countSessions(t); n != 1 {
		t.Errorf("user_sessions rows = %d, want 1 — failed logins must not mint sessions", n)
	}
}

func TestRefresh_DB_RotatesSessionHashes(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	rec := signupUser(t, r, "dave@example.com", "refresh-pass-1")
	r0 := cookieValue(t, rec, "refresh_token")

	refreshed, _ := doAuth(t, r, http.MethodPost, "/auth/refresh", nil, refreshCookie(r0), nil)
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh: status = %d (body %s)", refreshed.Code, refreshed.Body.String())
	}
	r1 := cookieValue(t, refreshed, "refresh_token")
	if r1 == r0 {
		t.Fatal("refresh must issue a NEW refresh token (rotation)")
	}
	newAccess := cookieValue(t, refreshed, "access_token")

	session := sessionByRefresh(t, r1)
	if session.PrevRefreshTokenHash != models.HashToken(r0) {
		t.Error("rotation must move the presented hash to prev_refresh_token_hash for reuse detection")
	}
	if session.AccessTokenHash != models.HashToken(newAccess) {
		t.Error("rotation must persist the new access token hash")
	}
	if session.RotatedAt == nil {
		t.Error("rotation must stamp rotated_at — the grace window has no anchor without it")
	}
	if n := countSessions(t); n != 1 {
		t.Errorf("user_sessions rows = %d, want 1 — rotation updates the row in place", n)
	}
}

// TestRefresh_DB_GraceReplayDoesNotRotate pins ClassifyRefresh's grace
// semantics end-to-end: a concurrent tab replaying the just-superseded token
// inside the grace window gets a fresh access token but no second rotation.
func TestRefresh_DB_GraceReplayDoesNotRotate(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	rec := signupUser(t, r, "erin@example.com", "grace-pass-12")
	r0 := cookieValue(t, rec, "refresh_token")

	rotated, _ := doAuth(t, r, http.MethodPost, "/auth/refresh", nil, refreshCookie(r0), nil)
	if rotated.Code != http.StatusOK {
		t.Fatalf("first refresh: status = %d", rotated.Code)
	}
	r1 := cookieValue(t, rotated, "refresh_token")
	before := sessionByRefresh(t, r1)

	// Replay r0 well inside the default 30s grace window.
	replay, _ := doAuth(t, r, http.MethodPost, "/auth/refresh", nil, refreshCookie(r0), nil)
	if replay.Code != http.StatusOK {
		t.Fatalf("grace replay: status = %d, want 200 (body %s)", replay.Code, replay.Body.String())
	}
	if cookieValue(t, replay, "access_token") == "" {
		t.Error("grace replay must still issue a fresh access token")
	}
	for _, ck := range replay.Result().Cookies() {
		if ck.Name == "refresh_token" {
			t.Errorf("grace replay set a refresh cookie (%q) — the winner's cookie is authoritative", ck.Value)
		}
	}
	after := sessionByRefresh(t, r1)
	if after.RefreshTokenHash != before.RefreshTokenHash || after.PrevRefreshTokenHash != before.PrevRefreshTokenHash {
		t.Error("grace replay must leave the session's hashes untouched")
	}
	if after.RevokedAt != nil {
		t.Error("grace replay must not revoke the session")
	}
}

// TestRefresh_DB_ReuseAfterGraceRevokesSession pins reuse detection and the
// ACTUAL scope of handleRefreshReuse: it soft-revokes the ONE session whose
// token was replayed (revoked_at set, both of its tokens dead) — despite the
// "revoke the session family" phrasing on OutcomeReuse, the user's OTHER
// sessions stay live. NOTE: reported upstream — if family-wide revocation is
// the intent, this test pins the gap.
func TestRefresh_DB_ReuseAfterGraceRevokesSession(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	const password = "reuse-pass-123"
	rec := signupUser(t, r, "frank@example.com", password)
	r0 := cookieValue(t, rec, "refresh_token")

	// A second, independent session for the same user (another device).
	other, _ := doAuth(t, r, http.MethodPost, "/auth/login", map[string]any{
		"email": "frank@example.com", "password": password,
	}, nil, nil)
	otherRefresh := cookieValue(t, other, "refresh_token")

	rotated, _ := doAuth(t, r, http.MethodPost, "/auth/refresh", nil, refreshCookie(r0), nil)
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotation: status = %d", rotated.Code)
	}
	r1 := cookieValue(t, rotated, "refresh_token")
	session := sessionByRefresh(t, r1)

	// Push the rotation outside the 30s grace window without sleeping.
	if err := models.DB.Model(&models.UserSession{}).Where("id = ?", session.ID).
		Update("rotated_at", time.Now().Add(-2*time.Minute)).Error; err != nil {
		t.Fatalf("backdate rotated_at: %v", err)
	}

	// The replay of the retired token is now theft. Same generic 401 as every
	// other failure, cookies cleared, session soft-revoked.
	reuse, env := doAuth(t, r, http.MethodPost, "/auth/refresh", nil, refreshCookie(r0), nil)
	if reuse.Code != http.StatusUnauthorized {
		t.Fatalf("reuse replay: status = %d, want 401 (body %s)", reuse.Code, reuse.Body.String())
	}
	if env.Error == nil || env.Error.Code != "INVALID_REFRESH_TOKEN" {
		t.Errorf("error = %+v, want the uniform INVALID_REFRESH_TOKEN", env.Error)
	}
	if !hasClearedCookie(reuse, "access_token") || !hasClearedCookie(reuse, "refresh_token") {
		t.Error("reuse detection must clear both auth cookies")
	}

	var revoked models.UserSession
	if err := models.DB.First(&revoked, "id = ?", session.ID).Error; err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("reuse detection must set revoked_at on the replayed session")
	}

	// The CURRENT token of the revoked session is dead too: any replay
	// against a revoked session classifies as reuse.
	current, _ := doAuth(t, r, http.MethodPost, "/auth/refresh", nil, refreshCookie(r1), nil)
	if current.Code != http.StatusUnauthorized {
		t.Errorf("current-token refresh on revoked session: status = %d, want 401", current.Code)
	}

	// Actual scope pin: the user's other session was NOT revoked.
	otherSession := sessionByRefresh(t, otherRefresh)
	if otherSession.RevokedAt != nil {
		t.Error("handleRefreshReuse revoked an unrelated session — behavior changed, update the scope pin")
	}
	otherRec, _ := doAuth(t, r, http.MethodPost, "/auth/refresh", nil, refreshCookie(otherRefresh), nil)
	if otherRec.Code != http.StatusOK {
		t.Errorf("other session refresh: status = %d — reuse handling is per-session today, not family-wide", otherRec.Code)
	}
}

func TestLogout_DB_DeletesSessionAndBlocksRefresh(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	rec := signupUser(t, r, "grace@example.com", "logout-pass-1")
	access := cookieValue(t, rec, "access_token")
	refresh := cookieValue(t, rec, "refresh_token")

	out, _ := doAuth(t, r, http.MethodPost, "/auth/logout", nil, accessCookie(access), nil)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout: status = %d, want 204 (body %s)", out.Code, out.Body.String())
	}
	if !hasClearedCookie(out, "access_token") || !hasClearedCookie(out, "refresh_token") {
		t.Error("logout must clear both auth cookies")
	}
	if n := countSessions(t); n != 0 {
		t.Fatalf("user_sessions rows = %d, want 0 — logout deletes the session row", n)
	}

	// The refresh token from the revoked session must be dead.
	rec2, env := doAuth(t, r, http.MethodPost, "/auth/refresh", nil, refreshCookie(refresh), nil)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout refresh: status = %d, want 401", rec2.Code)
	}
	if env.Error == nil || env.Error.Code != "INVALID_REFRESH_TOKEN" {
		t.Errorf("error = %+v, want INVALID_REFRESH_TOKEN", env.Error)
	}
}

func TestMe_DB_ReturnsProfileForValidToken(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	rec := signupUser(t, r, "heidi@example.com", "profile-pass-1")
	access := cookieValue(t, rec, "access_token")

	me, env := doAuth(t, r, http.MethodGet, "/auth/me", nil, accessCookie(access), nil)
	if me.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", me.Code, me.Body.String())
	}
	userData, _ := env.Data["user"].(map[string]any)
	if userData == nil || userData["email"] != "heidi@example.com" {
		t.Errorf("data.user = %v, want the caller's profile", env.Data["user"])
	}
	if strings.Contains(strings.ToLower(me.Body.String()), "passwordhash") {
		t.Error("/auth/me leaks the password hash field")
	}
}

// TestInternalRevokeUserSessions_DB drives the internal revocation endpoint
// over committed rows: every active session for the user is deleted and its
// refresh tokens stop working.
func TestInternalRevokeUserSessions_DB(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	const password = "revoke-pass-12"
	signupUser(t, r, "ivan@example.com", password)
	login, _ := doAuth(t, r, http.MethodPost, "/auth/login", map[string]any{
		"email": "ivan@example.com", "password": password,
	}, nil, nil)
	refresh := cookieValue(t, login, "refresh_token")
	user := loadOnlyUser(t)
	if n := countSessions(t); n != 2 {
		t.Fatalf("precondition: sessions = %d, want 2", n)
	}

	rec, env := doAuth(t, r, http.MethodPost, "/internal/users/"+user.ID.String()+"/revoke-sessions",
		nil, nil, map[string]string{authverify.InternalTokenHeader: testInternalToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if got, _ := env.Data["revokedCount"].(float64); got != 2 {
		t.Errorf("revokedCount = %v, want 2", env.Data["revokedCount"])
	}
	if n := countSessions(t); n != 0 {
		t.Errorf("user_sessions rows = %d, want 0 after revocation", n)
	}

	blocked, _ := doAuth(t, r, http.MethodPost, "/auth/refresh", nil, refreshCookie(refresh), nil)
	if blocked.Code != http.StatusUnauthorized {
		t.Errorf("refresh after revocation: status = %d, want 401", blocked.Code)
	}
}

// TestInternalGetUserPlan_DB pins both plan resolution paths of the internal
// API: a real subscription_plans row is served verbatim, a user whose plan
// has no catalogue row falls back to the hardcoded free limits, and a missing
// user is a 404 (not a silent free fallback).
func TestInternalGetUserPlan_DB(t *testing.T) {
	withDB(t)
	r := newAuthRouter(t)
	signupUser(t, r, "judy@example.com", "plan-pass-123")
	user := loadOnlyUser(t)
	internalHeader := map[string]string{authverify.InternalTokenHeader: testInternalToken}

	t.Run("plan resolved from catalogue row", func(t *testing.T) {
		pro := models.SubscriptionPlan{Name: "pro", MaxFileSizeMB: 500, MaxFilesPerJob: 50, RetentionDays: 30}
		if err := models.DB.Create(&pro).Error; err != nil {
			t.Fatalf("seed pro plan: %v", err)
		}
		if err := models.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("plan_name", "pro").Error; err != nil {
			t.Fatalf("assign pro plan: %v", err)
		}
		resetPlanCache(t)

		rec, env := doAuth(t, r, http.MethodGet, "/internal/users/"+user.ID.String()+"/plan", nil, nil, internalHeader)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
		plan, _ := env.Data["plan"].(map[string]any)
		if plan == nil || plan["name"] != "pro" || plan["maxFileSizeMb"] != float64(500) {
			t.Errorf("plan = %v, want the seeded pro row", env.Data["plan"])
		}
	})

	t.Run("missing catalogue row falls back to free limits", func(t *testing.T) {
		if err := models.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("plan_name", "free").Error; err != nil {
			t.Fatalf("assign free plan: %v", err)
		}
		resetPlanCache(t) // subscription_plans has no "free" row in this test DB

		rec, env := doAuth(t, r, http.MethodGet, "/internal/users/"+user.ID.String()+"/plan", nil, nil, internalHeader)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
		plan, _ := env.Data["plan"].(map[string]any)
		if plan == nil || plan["name"] != "free" || plan["maxFileSizeMb"] != float64(25) {
			t.Errorf("plan = %v, want the hardcoded free fallback (25MB)", env.Data["plan"])
		}
	})

	t.Run("missing user is 404", func(t *testing.T) {
		rec, env := doAuth(t, r, http.MethodGet, "/internal/users/"+uuid.NewString()+"/plan", nil, nil, internalHeader)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 — an unknown user must not get a silent free plan", rec.Code)
		}
		if env.Error == nil || env.Error.Code != "NOT_FOUND" {
			t.Errorf("error = %+v, want NOT_FOUND", env.Error)
		}
	})
}
