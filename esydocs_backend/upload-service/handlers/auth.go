package handlers

import (
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func authUserID(c *gin.Context) *uuid.UUID {
	if c == nil {
		return nil
	}
	if header := c.GetHeader("X-User-ID"); header != "" {
		if parsed, err := uuid.Parse(strings.TrimSpace(header)); err == nil {
			return &parsed
		}
	}

	secret := os.Getenv("JWT_SECRET")
	authHeader := c.GetHeader("Authorization")
	if secret == "" || authHeader == "" {
		return nil
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return nil
	}
	tokenString := strings.TrimSpace(parts[1])
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return nil
	}
	if claims.Subject == "" {
		return nil
	}
	parsed, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil
	}
	return &parsed
}
