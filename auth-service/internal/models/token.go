package models

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UserSession stores one login session with both access and refresh token hashes.
// One row = one login.
type UserSession struct {
	ID               uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID           uuid.UUID  `gorm:"type:uuid;not null;index:idx_user_sessions_user_id" json:"userId"`
	AccessTokenHash  string     `gorm:"type:text;not null;uniqueIndex:idx_user_sessions_access_hash" json:"-"`
	RefreshTokenHash string     `gorm:"type:text;uniqueIndex:idx_user_sessions_refresh_hash" json:"-"`
	AccessExpiresAt  time.Time  `gorm:"not null" json:"accessExpiresAt"`
	RefreshExpiresAt *time.Time `gorm:"" json:"refreshExpiresAt,omitempty"`
	CreatedAt        time.Time  `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (s *UserSession) BeforeCreate(tx *gorm.DB) (err error) {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
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

// FindSessionByRefreshHash finds an active session by its refresh token hash.
func FindSessionByRefreshHash(db *gorm.DB, refreshHash string) (*UserSession, error) {
	var session UserSession
	err := db.Where("refresh_token_hash = ? AND refresh_expires_at > ?", refreshHash, time.Now()).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// RevokeSessionByAccessHash deletes the session matching the given access token hash.
func RevokeSessionByAccessHash(db *gorm.DB, accessTokenHash string) error {
	return db.Where("access_token_hash = ?", accessTokenHash).Delete(&UserSession{}).Error
}

// RevokeAllUserSessions deletes all active sessions for a user. Returns the deleted sessions.
func RevokeAllUserSessions(db *gorm.DB, userID uuid.UUID) ([]UserSession, error) {
	now := time.Now()
	var sessions []UserSession
	if err := db.Where("user_id = ? AND (access_expires_at > ? OR (refresh_expires_at IS NOT NULL AND refresh_expires_at > ?))", userID, now, now).Find(&sessions).Error; err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, nil
	}
	if err := db.Where("user_id = ? AND (access_expires_at > ? OR (refresh_expires_at IS NOT NULL AND refresh_expires_at > ?))", userID, now, now).Delete(&UserSession{}).Error; err != nil {
		return nil, err
	}
	return sessions, nil
}

// DeleteExpiredSessions removes sessions where both access and refresh tokens have expired.
func DeleteExpiredSessions(db *gorm.DB) (int64, error) {
	now := time.Now()
	tx := db.Where("access_expires_at < ? AND (refresh_expires_at IS NULL OR refresh_expires_at < ?)", now, now).Delete(&UserSession{})
	return tx.RowsAffected, tx.Error
}
