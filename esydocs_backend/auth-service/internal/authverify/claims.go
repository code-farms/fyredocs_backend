package authverify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type ScopeList []string

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

type Claims struct {
	jwt.RegisteredClaims
	Role    string    `json:"role,omitempty"`
	Scope   ScopeList `json:"scope,omitempty"`
	Plan    string    `json:"plan,omitempty"`
	IsGuest bool      `json:"is_guest,omitempty"`
}

func (c Claims) ToAuthContext() AuthContext {
	scope := make([]string, 0, len(c.Scope))
	for _, entry := range c.Scope {
		if strings.TrimSpace(entry) != "" {
			scope = append(scope, entry)
		}
	}
	return AuthContext{
		UserID:  strings.TrimSpace(c.Subject),
		Role:    strings.TrimSpace(c.Role),
		Scope:   scope,
		IsGuest: c.IsGuest,
	}
}
