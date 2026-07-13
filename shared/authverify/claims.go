package authverify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// ScopeList is a set of scope strings that tolerates either JSON encoding used
// by token issuers: a space/comma-delimited string or a JSON array.
type ScopeList []string

// UnmarshalJSON accepts a scope claim as either a delimited string or a string
// array, normalizing both to a trimmed, non-empty slice.
func (s *ScopeList) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*s = nil
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		*s = splitScope(asString)
		return nil
	}
	var asList []string
	if err := json.Unmarshal(data, &asList); err == nil {
		clean := make([]string, 0, len(asList))
		for _, item := range asList {
			item = strings.TrimSpace(item)
			if item != "" {
				clean = append(clean, item)
			}
		}
		*s = clean
		return nil
	}
	return fmt.Errorf("invalid scope claim")
}

func splitScope(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t' || r == '\n'
	})
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	return clean
}

// Claims is the verifier's view of a token: the standard registered claims plus
// the platform's authorization fields (role, scope, guest flag, impersonation).
type Claims struct {
	jwt.RegisteredClaims
	Role           string    `json:"role,omitempty"`
	Scope          ScopeList `json:"scope,omitempty"`
	IsGuest        bool      `json:"is_guest,omitempty"`
	ImpersonatedBy string    `json:"impersonated_by,omitempty"`
}

// ToAuthContext projects verified claims into the AuthContext carried through
// request handling, trimming whitespace and dropping empty scopes.
func (c Claims) ToAuthContext() AuthContext {
	scope := make([]string, 0, len(c.Scope))
	for _, entry := range c.Scope {
		if strings.TrimSpace(entry) != "" {
			scope = append(scope, entry)
		}
	}
	return AuthContext{
		UserID:         strings.TrimSpace(c.Subject),
		Role:           strings.TrimSpace(c.Role),
		Scope:          scope,
		IsGuest:        c.IsGuest,
		ImpersonatedBy: strings.TrimSpace(c.ImpersonatedBy),
	}
}
