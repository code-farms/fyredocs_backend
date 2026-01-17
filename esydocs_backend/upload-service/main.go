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

func main() {
	config.LoadConfig()
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
	denylistEnabled := getEnvBool("AUTH_DENYLIST_ENABLED", false)
	guestPrefix := os.Getenv("AUTH_GUEST_PREFIX")
	guestSuffix := os.Getenv("AUTH_GUEST_SUFFIX")
	trustGateway := getEnvBool("AUTH_TRUST_GATEWAY_HEADERS", false)

	var denylist auth.TokenDenylist
	if denylistEnabled {
		denylist = auth.NewRedisTokenDenylist(redisstore.Client, os.Getenv("AUTH_DENYLIST_PREFIX"))
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
