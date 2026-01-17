package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

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
