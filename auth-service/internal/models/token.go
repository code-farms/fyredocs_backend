package models

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UserSession stores one login session with both access and refresh token hashes.
// One row = one login. Refresh rotation updates the row in place: on every
// successful refresh the previous refresh hash moves to PrevRefreshTokenHash so
// a replay of the superseded token is detectable (reuse detection), and
// RevokedAt soft-revokes the session without deleting the evidence.
type UserSession struct {
	ID               uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	UserID           uuid.UUID `gorm:"type:uuid;not null;constraint:OnDelete:CASCADE" json:"userId"`
	User             *User     `gorm:"foreignKey:UserID" json:"-"`
	AccessTokenHash  string    `gorm:"type:text;not null;uniqueIndex:idx_user_sessions_access_hash" json:"-"`
	RefreshTokenHash string    `gorm:"type:text;uniqueIndex:idx_user_sessions_refresh_hash" json:"-"`
	// PrevRefreshTokenHash holds the immediately superseded refresh hash after a
	// rotation. A refresh attempt matching only this column and outside the
	// rotation grace window is token reuse.
	PrevRefreshTokenHash string     `gorm:"type:text;index:idx_user_sessions_prev_refresh_hash" json:"-"`
	AccessExpiresAt      time.Time  `gorm:"not null" json:"accessExpiresAt"`
	RefreshExpiresAt     *time.Time `gorm:"" json:"refreshExpiresAt,omitempty"`
	// RotatedAt is the time of the most recent refresh rotation; nil until the
	// first rotation.
	RotatedAt *time.Time `gorm:"" json:"-"`
	// RevokedAt soft-revokes the session (reuse detection, admin revocation).
	// Revoked rows are kept for 24h so repeated replays stay classifiable, then
	// reaped by DeleteExpiredSessions.
	RevokedAt *time.Time `gorm:"index:idx_user_sessions_revoked_at" json:"-"`
	CreatedAt time.Time  `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (s *UserSession) BeforeCreate(tx *gorm.DB) (err error) {
	if s.ID == uuid.Nil {
		s.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

// HashToken returns the hex-encoded SHA-256 hash of a raw token string.
func HashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// StoreSession inserts a new session row for the given login with both tokens.
func StoreSession(db *gorm.DB, sessionID, userID uuid.UUID, accessToken string, accessExpiresAt time.Time, refreshToken string, refreshExpiresAt time.Time) error {
	session := UserSession{
		ID:               sessionID,
		UserID:           userID,
		AccessTokenHash:  HashToken(accessToken),
		RefreshTokenHash: HashToken(refreshToken),
		AccessExpiresAt:  accessExpiresAt,
		RefreshExpiresAt: &refreshExpiresAt,
	}
	return db.Create(&session).Error
}

// StoreAccessOnlySession inserts a session that has only an access token and no
// refresh token, used for short-lived proxy-login (impersonation) sessions.
// refresh_token_hash is omitted so it is stored as NULL — the unique index on
// that column treats NULLs as distinct, so multiple access-only sessions can
// coexist. The session row still makes the impersonation revocable.
func StoreAccessOnlySession(db *gorm.DB, sessionID, userID uuid.UUID, accessToken string, accessExpiresAt time.Time) error {
	session := UserSession{
		ID:              sessionID,
		UserID:          userID,
		AccessTokenHash: HashToken(accessToken),
		AccessExpiresAt: accessExpiresAt,
	}
	return db.Omit("RefreshTokenHash", "RefreshExpiresAt").Create(&session).Error
}

// FindSessionByRefreshHash finds an unexpired session by its current refresh
// token hash. Revoked rows are still returned so the caller can classify a
// replay against a revoked session as reuse rather than a generic miss.
func FindSessionByRefreshHash(db *gorm.DB, refreshHash string) (*UserSession, error) {
	var session UserSession
	err := db.Where("refresh_token_hash = ? AND refresh_expires_at > ?", refreshHash, time.Now()).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// FindSessionByPrevRefreshHash finds an unexpired session whose immediately
// superseded refresh hash matches. Used by reuse detection and the concurrent-
// refresh grace window; revoked rows are returned for the same reason as above.
func FindSessionByPrevRefreshHash(db *gorm.DB, refreshHash string) (*UserSession, error) {
	var session UserSession
	err := db.Where("prev_refresh_token_hash = ? AND refresh_expires_at > ?", refreshHash, time.Now()).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// RotateSessionTokens atomically rotates a session's token pair: the current
// refresh hash moves to prev_refresh_token_hash and the new hashes/expiries are
// written, guarded by `refresh_token_hash = oldRefreshHash AND revoked_at IS
// NULL` so exactly one of two concurrent refreshes wins. Returns false (with no
// error) when the guard did not match — the caller should re-read the session
// and take the grace path.
func RotateSessionTokens(db *gorm.DB, sessionID uuid.UUID, oldRefreshHash, newAccessHash string, accessExpiresAt time.Time, newRefreshHash string, refreshExpiresAt time.Time) (bool, error) {
	now := time.Now()
	tx := db.Model(&UserSession{}).
		Where("id = ? AND refresh_token_hash = ? AND revoked_at IS NULL", sessionID, oldRefreshHash).
		Updates(map[string]any{
			"prev_refresh_token_hash": oldRefreshHash,
			"refresh_token_hash":      newRefreshHash,
			"refresh_expires_at":      refreshExpiresAt,
			"access_token_hash":       newAccessHash,
			"access_expires_at":       accessExpiresAt,
			"rotated_at":              now,
		})
	if tx.Error != nil {
		return false, tx.Error
	}
	return tx.RowsAffected > 0, nil
}

// MarkSessionRevoked soft-revokes a session (sets revoked_at). The row is kept
// so later replays of either token hash are still classifiable as reuse; it is
// reaped by DeleteExpiredSessions after 24h.
func MarkSessionRevoked(db *gorm.DB, sessionID uuid.UUID) error {
	return db.Model(&UserSession{}).
		Where("id = ? AND revoked_at IS NULL", sessionID).
		Update("revoked_at", time.Now()).Error
}

// RevokeSessionByAccessHash deletes the session matching the given access token hash.
func RevokeSessionByAccessHash(db *gorm.DB, accessTokenHash string) error {
	return db.Where("access_token_hash = ?", accessTokenHash).Delete(&UserSession{}).Error
}

// RevokeAllUserSessions deletes all active sessions for a user. Returns the deleted sessions.
func RevokeAllUserSessions(db *gorm.DB, userID uuid.UUID) ([]UserSession, error) {
	now := time.Now()
	var sessions []UserSession
	err := db.Raw(
		`DELETE FROM user_sessions WHERE user_id = ? AND (access_expires_at > ? OR (refresh_expires_at IS NOT NULL AND refresh_expires_at > ?)) RETURNING *`,
		userID, now, now,
	).Scan(&sessions).Error
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

// DeleteExpiredSessions removes sessions where both access and refresh tokens
// have expired, plus soft-revoked sessions older than 24h (kept that long only
// so token replays against them stay classifiable as reuse).
func DeleteExpiredSessions(db *gorm.DB) (int64, error) {
	now := time.Now()
	tx := db.Where(
		"(access_expires_at < ? AND (refresh_expires_at IS NULL OR refresh_expires_at < ?)) OR revoked_at < ?",
		now, now, now.Add(-24*time.Hour),
	).Delete(&UserSession{})
	return tx.RowsAffected, tx.Error
}
