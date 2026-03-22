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
	PlanName     string    `gorm:"type:text;not null;default:'free'" json:"planName"`
	Role         string    `gorm:"type:text;not null;default:'user'" json:"role"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (u *User) BeforeCreate(tx *gorm.DB) (err error) {
	if u.ID == uuid.Nil {
		u.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

type AuthMetadata struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID      uuid.UUID  `gorm:"type:uuid;not null;index;constraint:OnDelete:CASCADE" json:"userId"`
	User        *User      `gorm:"foreignKey:UserID" json:"-"`
	Provider    string     `gorm:"type:text;not null" json:"provider"`
	Subject     string     `gorm:"type:text;not null" json:"subject"`
	LastLoginAt *time.Time `gorm:"" json:"lastLoginAt,omitempty"`
	CreatedAt   time.Time  `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (a *AuthMetadata) BeforeCreate(tx *gorm.DB) (err error) {
	if a.ID == uuid.Nil {
		a.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

type SubscriptionPlan struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Name           string    `gorm:"type:text;unique;not null" json:"name"`
	MaxFileSizeMB  int       `gorm:"not null" json:"maxFileSizeMb"`
	MaxFilesPerJob int       `gorm:"not null" json:"maxFilesPerJob"`
	RetentionDays  int       `gorm:"not null" json:"retentionDays"`
	CreatedAt      time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (p *SubscriptionPlan) BeforeCreate(tx *gorm.DB) (err error) {
	if p.ID == uuid.Nil {
		p.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}
