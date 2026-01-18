package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"upload-service/auth"
	"upload-service/config"
	"upload-service/database"
	"upload-service/redisstore"
	"upload-service/routes"
)

func validateJWTSecret() error {
	secret := os.Getenv("JWT_HS256_SECRET")
	if secret == "" {
		secret = os.Getenv("JWT_SECRET")
	}

	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("JWT_HS256_SECRET environment variable is required but not set")
	}

	// Minimum entropy check: 32 bytes (256 bits) for HS256
	if len(secret) < 32 {
		return fmt.Errorf("JWT_HS256_SECRET must be at least 32 characters (256 bits), got %d characters", len(secret))
	}

	// Check if it's the example/default secret (security smell)
	dangerousSecrets := []string{
		"4de0ea7311594deb860f03e5da60ac903fc4b4099bfe499a82e0fed013af32ca791ac065ea5e4d8aaade24a760e6dc58",
		"change-me",
		"secret",
		"password",
	}
	for _, dangerous := range dangerousSecrets {
		if secret == dangerous {
			return fmt.Errorf("JWT_HS256_SECRET appears to be a default/example value - use a cryptographically random secret")
		}
	}

	log.Println("JWT secret validation passed")
	return nil
}

func main() {
	config.LoadConfig()

	// SECURITY: Validate JWT secret is set and meets minimum requirements
	if err := validateJWTSecret(); err != nil {
		panic(fmt.Sprintf("JWT secret validation failed: %v\n\nFor local development:\n  1. Copy .env.example to .env\n  2. Generate a secret: openssl rand -hex 32\n  3. Set JWT_HS256_SECRET in .env\n\nFor production:\n  Set environment variable: export JWT_HS256_SECRET=\"your-secret-here\"", err))
	}

	database.Connect()
	database.Migrate()
	redisstore.Connect()

	r := gin.Default()
	if err := r.SetTrustedProxies(trustedProxies()); err != nil {
		panic(err)
	}
	authMiddleware := buildAuthMiddleware()
	r.Use(authMiddleware)
	routes.SetupUploadRouter(r)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	if err := r.Run(fmt.Sprintf(":%s", port)); err != nil {
		panic(err)
	}
}

func trustedProxies() []string {
	raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES"))
	if raw == "" {
		return []string{"127.0.0.1", "::1"}
	}

	parts := strings.Split(raw, ",")
	proxies := make([]string, 0, len(parts))
	for _, part := range parts {
		proxy := strings.TrimSpace(part)
		if proxy != "" {
			proxies = append(proxies, proxy)
		}
	}
	if len(proxies) == 0 {
		return []string{"127.0.0.1", "::1"}
	}

	return proxies
}

func buildAuthMiddleware() gin.HandlerFunc {
	denylistEnabled := getEnvBool("AUTH_DENYLIST_ENABLED", true)
	guestPrefix := os.Getenv("AUTH_GUEST_PREFIX")
	guestSuffix := os.Getenv("AUTH_GUEST_SUFFIX")
	trustGateway := getEnvBool("AUTH_TRUST_GATEWAY_HEADERS", false)

	var denylist auth.TokenDenylist
	if denylistEnabled {
		denylist = auth.NewRedisTokenDenylist(redisstore.Client, os.Getenv("AUTH_DENYLIST_PREFIX"))
		if denylist == nil {
			log.Println("WARNING: Token denylist enabled but Redis unavailable - logout will not revoke access tokens")
		} else {
			log.Println("Token denylist enabled - access tokens will be revoked on logout")
		}
	} else {
		log.Println("WARNING: Token denylist disabled - logged-out users can still use access tokens until expiration (15 minutes)")
	}

	verifier, err := auth.NewVerifierFromEnv(denylist)
	if err != nil {
		log.Fatalf("auth verifier init failed: %v", err)
	}

	guestStore := auth.NewRedisGuestStore(redisstore.Client, auth.GuestStoreConfig{
		KeyPrefix: guestPrefix,
		KeySuffix: guestSuffix,
	})

	return auth.GinAuthMiddleware(auth.GinMiddlewareOptions{
		Verifier:            verifier,
		GuestStore:          guestStore,
		TrustGatewayHeaders: trustGateway,
	})
}

func getEnvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	default:
		return fallback
	}
}
