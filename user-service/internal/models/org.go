package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RBAC roles, highest to lowest privilege.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

var roleRank = map[string]int{RoleViewer: 1, RoleEditor: 2, RoleAdmin: 3, RoleOwner: 4}

// RoleAtLeast reports whether `role` meets or exceeds `min`.
func RoleAtLeast(role, min string) bool {
	return roleRank[role] >= roleRank[min] && roleRank[role] > 0
}

// ValidRole reports whether the string is a known role.
func ValidRole(role string) bool {
	return roleRank[role] > 0
}

// Organization is a tenant that owns documents, members, and billing.
type Organization struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Name        string    `gorm:"type:text;not null" json:"name"`
	Slug        string    `gorm:"type:text;uniqueIndex;not null" json:"slug"`
	OwnerUserID uuid.UUID `gorm:"type:uuid;not null;index" json:"ownerUserId"`
	PlanName    string    `gorm:"type:text;not null;default:'free'" json:"planName"`
	CreatedAt   time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
	UpdatedAt   time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"updatedAt"`
}

func (o *Organization) BeforeCreate(tx *gorm.DB) error {
	if o.ID == uuid.Nil {
		o.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}

// Membership ties a user to an organization with a role (the RBAC anchor).
type Membership struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_membership_org_user,priority:1" json:"organizationId"`
	UserID         uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_membership_org_user,priority:2" json:"userId"`
	Role           string    `gorm:"type:text;not null;default:'viewer'" json:"role"`
	CreatedAt      time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"createdAt"`
	UpdatedAt      time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"updatedAt"`
}

func (m *Membership) BeforeCreate(tx *gorm.DB) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.Must(uuid.NewV7())
	}
	return nil
}
