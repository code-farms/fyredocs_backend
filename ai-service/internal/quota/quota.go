// Package quota is ai-service's per-subject monthly token ledger. An op
// RESERVES its worst case (estimated input + the output cap) atomically
// before calling the provider and SETTLES to the provider's real usage after,
// so concurrent requests cannot jointly overrun a limit — the check and the
// increment are one SQL statement.
//
// Subject is "user:<id>" today. Quota is documented as per-org, but the
// gateway forwards no org identity yet; the subject column is text so
// "org:<id>" needs no migration once it does.
package quota

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"fyredocs/shared/config"

	"ai-service/internal/llm"
)

// ErrExceeded means the reservation would push the subject over its limit.
var ErrExceeded = errors.New("ai quota exceeded")

// Limits is the monthly token allowance per plan. A zero limit disables AI
// for that plan (every reservation is ErrExceeded).
type Limits struct {
	Free int64
	Pro  int64
}

// LimitsFromEnv reads AI_QUOTA_FREE_TOKENS and AI_QUOTA_PRO_TOKENS.
func LimitsFromEnv() Limits {
	return Limits{
		Free: int64(config.GetEnvInt("AI_QUOTA_FREE_TOKENS", 300_000)),
		Pro:  int64(config.GetEnvInt("AI_QUOTA_PRO_TOKENS", 5_000_000)),
	}
}

// For returns the limit for a plan name; unknown plans get the free limit,
// the same fallback the gateway's rate limiter uses.
func (l Limits) For(plan string) int64 {
	if strings.EqualFold(strings.TrimSpace(plan), "pro") {
		return l.Pro
	}
	return l.Free
}

// Period is the UTC month as YYYYMM.
func Period(t time.Time) int {
	t = t.UTC()
	return t.Year()*100 + int(t.Month())
}

// periodLabel renders a period as "YYYY-MM".
func periodLabel(p int) string { return fmt.Sprintf("%04d-%02d", p/100, p%100) }

// resetsAt is the first instant of the month after period p.
func resetsAt(p int) time.Time {
	return time.Date(p/100, time.Month(p%100), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
}

// Subject keys a user's ledger rows.
func Subject(userID string) string { return "user:" + userID }

// Reservation is an outstanding hold; Settle releases it exactly once.
type Reservation struct {
	Subject string
	Period  int
	Tokens  int64
}

// Status is the caller-facing quota summary.
type Status struct {
	Plan            string    `json:"plan"`
	Period          string    `json:"period"`
	LimitTokens     int64     `json:"limitTokens"`
	UsedTokens      int64     `json:"usedTokens"`
	ReservedTokens  int64     `json:"reservedTokens"`
	RemainingTokens int64     `json:"remainingTokens"`
	ResetsAt        time.Time `json:"resetsAt"`
}

// Ledger implements reservations over the ai_usage table.
type Ledger struct {
	DB     *gorm.DB
	Limits Limits
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

// now returns the ledger clock.
func (l *Ledger) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// reserveSQL inserts or increments the hold only while used + held + new
// stays within the limit. A refused ON CONFLICT ... WHERE affects 0 rows,
// which is how an over-limit request is detected without a race window.
const reserveSQL = `
INSERT INTO ai_usage (subject, period, reserved_tokens, updated_at)
VALUES (?, ?, ?, now())
ON CONFLICT (subject, period) DO UPDATE
SET reserved_tokens = ai_usage.reserved_tokens + EXCLUDED.reserved_tokens, updated_at = now()
WHERE ai_usage.input_tokens + ai_usage.output_tokens + ai_usage.reserved_tokens + EXCLUDED.reserved_tokens <= ?`

// Reserve holds tokens against userID's limit for the current month.
func (l *Ledger) Reserve(ctx context.Context, userID, plan string, tokens int64) (Reservation, error) {
	limit := l.Limits.For(plan)
	r := Reservation{Subject: Subject(userID), Period: Period(l.now()), Tokens: tokens}
	if tokens > limit {
		return r, ErrExceeded
	}
	res := l.DB.WithContext(ctx).Exec(reserveSQL, r.Subject, r.Period, tokens, limit)
	if res.Error != nil {
		return r, fmt.Errorf("quota reserve: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return r, ErrExceeded
	}
	return r, nil
}

// Settle releases the hold and records real usage. Input and output both
// count toward the limit; cached tokens are recorded for visibility (they are
// part of input). The period is the reservation's, so a request that crosses
// midnight at month end settles where it was reserved.
func (l *Ledger) Settle(ctx context.Context, r Reservation, u llm.Usage) error {
	return l.DB.WithContext(ctx).Exec(`
UPDATE ai_usage
SET reserved_tokens = GREATEST(reserved_tokens - ?, 0),
    input_tokens = input_tokens + ?,
    output_tokens = output_tokens + ?,
    cached_tokens = cached_tokens + ?,
    updated_at = now()
WHERE subject = ? AND period = ?`,
		r.Tokens, u.InputTokens, u.OutputTokens, u.CachedTokens, r.Subject, r.Period).Error
}

// Status summarises userID's current month.
func (l *Ledger) Status(ctx context.Context, userID, plan string) (Status, error) {
	p := Period(l.now())
	var row struct {
		Used     int64
		Reserved int64
	}
	err := l.DB.WithContext(ctx).Raw(
		`SELECT COALESCE(SUM(input_tokens + output_tokens), 0) AS used, COALESCE(SUM(reserved_tokens), 0) AS reserved
		 FROM ai_usage WHERE subject = ? AND period = ?`, Subject(userID), p).Scan(&row).Error
	if err != nil {
		return Status{}, fmt.Errorf("quota status: %w", err)
	}
	limit := l.Limits.For(plan)
	remaining := limit - row.Used - row.Reserved
	if remaining < 0 {
		remaining = 0
	}
	label := "free"
	if limit == l.Limits.Pro && strings.EqualFold(strings.TrimSpace(plan), "pro") {
		label = "pro"
	}
	return Status{
		Plan: label, Period: periodLabel(p),
		LimitTokens: limit, UsedTokens: row.Used, ReservedTokens: row.Reserved,
		RemainingTokens: remaining, ResetsAt: resetsAt(p),
	}, nil
}
