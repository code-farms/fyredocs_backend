package config

// Plan limit fallbacks: the ONE place in the backend that holds plan numbers.
//
// The live limits are the rows of auth-service's `subscription_plans` table.
// auth-service publishes them to Redis, the gateway forwards them as
// X-User-Plan-* headers, and job-service and the workers enforce them; the web
// and mobile apps read the same rows from /auth/plans. To change a limit on a
// running system, update the table (no deploy needed), e.g.
//
//	UPDATE subscription_plans SET max_ocr_pages = 50 WHERE name = 'free';
//
// It reaches the gateway within PLAN_CACHE_TTL (5 min) and each user at their
// next login or token refresh. For a value every environment should get, add a
// migration (auth-service/internal/migrations) and change the constant here too.
//
// These constants are used only:
//   - as the seed that creates the rows in a fresh database
//     (auth-service/internal/models/database.go seedPlans, insert-only);
//   - as fallbacks when the table or the cache cannot be read: auth-service's
//     lastResortPlan, the gateway's DefaultPlan/DefaultGuestPlan, and the
//     Guest*() env defaults below.
//
// So editing a value here does NOT change an existing database.
const (
	PlanGuestMaxFileMB     = 20
	PlanGuestMaxFiles      = 5
	PlanGuestMaxJobMB      = 100
	PlanGuestMaxOCRPages   = 10
	PlanGuestRetentionDays = 0

	PlanFreeMaxFileMB     = 50
	PlanFreeMaxFiles      = 10
	PlanFreeMaxJobMB      = 300
	PlanFreeMaxOCRPages   = 30
	PlanFreeRetentionDays = 7

	PlanProMaxFileMB     = 300
	PlanProMaxFiles      = 50
	PlanProMaxJobMB      = 300
	PlanProMaxOCRPages   = 200
	PlanProRetentionDays = 30
)
