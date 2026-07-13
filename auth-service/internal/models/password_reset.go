package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PasswordResetToken stores a single-use token used to reset a user's password.
// The raw token is sent to the user via email; only its SHA-256 hash is persisted.
type PasswordResetToken struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;index:idx_password_reset_tokens_user_id;constraint:OnDelete:CASCADE" json:"userId"`
	User      *User     `gorm:"foreignKey:UserID" json:"-"`
	TokenHash string    `gorm:"type:text;not null;uniqueIndex:idx_password_reset_tokens_token_hash" json:"-"`
	ExpiresAt time.Time `gorm:"not null;index:idx_password_reset_tokens_expires_at" json:"expiresAt"`
	CreatedAt time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
	RequestIP string    `gorm:"type:text" json:"-"`
}

// BeforeCreate assigns a time-ordered UUIDv7 primary key when one was not set.
func (p *PasswordResetToken) BeforeCreate(tx *gorm.DB) (err error) {
	if p.ID == uuid.Nil {
		p.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

// CreatePasswordResetToken inserts a row with the SHA-256 hash of rawToken.
func CreatePasswordResetToken(db *gorm.DB, userID uuid.UUID, rawToken string, ttl time.Duration, ip string) error {
	row := PasswordResetToken{
		UserID:    userID,
		TokenHash: HashToken(rawToken),
		ExpiresAt: time.Now().Add(ttl),
		RequestIP: ip,
	}
	return db.Create(&row).Error
}

// FindValidResetTokenByHash returns an unexpired reset token row matching tokenHash.
func FindValidResetTokenByHash(db *gorm.DB, tokenHash string) (*PasswordResetToken, error) {
	var row PasswordResetToken
	if err := db.Where("token_hash = ? AND expires_at > ?", tokenHash, time.Now()).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// DeleteResetTokensForUser removes every reset token belonging to a user.
// Called before issuing a new token and after a successful reset.
func DeleteResetTokensForUser(db *gorm.DB, userID uuid.UUID) error {
	return db.Where("user_id = ?", userID).Delete(&PasswordResetToken{}).Error
}

// DeleteExpiredResetTokens prunes rows whose expires_at is in the past.
func DeleteExpiredResetTokens(db *gorm.DB) (int64, error) {
	tx := db.Where("expires_at < ?", time.Now()).Delete(&PasswordResetToken{})
	return tx.RowsAffected, tx.Error
}
