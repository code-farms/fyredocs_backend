package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type User struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Email        string    `gorm:"type:text;unique;not null" json:"email"`
	FullName     string    `gorm:"type:text" json:"fullName,omitempty"`
	Phone        string    `gorm:"type:text" json:"phone,omitempty"`
	Country      string    `gorm:"type:text" json:"country,omitempty"`
	ImageURL     string    `gorm:"type:text" json:"imageUrl,omitempty"`
	PasswordHash string    `gorm:"type:text;not null" json:"-"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (u *User) BeforeCreate(tx *gorm.DB) (err error) {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return nil
}
