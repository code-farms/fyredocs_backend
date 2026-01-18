package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"

	"organize-pdf/config"
	"organize-pdf/database"
	"organize-pdf/redisstore"
	"organize-pdf/routes"
	"organize-pdf/worker"
)

func main() {
	config.LoadConfig()
	database.Connect()
	database.Migrate()
	redisstore.Connect()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Run(ctx)

	r := gin.New()
	if err := r.SetTrustedProxies(trustedProxies()); err != nil {
		panic(err)
	}
	r.GET("/healthz", func(c *gin.Context) {
		c.String(200, "ok")
	})

	routes.SetupOrganizePdfRouter(r)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8084"
	}

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- r.Run(fmt.Sprintf(":%s", port))
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-stop:
		cancel()
	case err := <-serverErr:
		if err != nil {
			panic(err)
		}
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
