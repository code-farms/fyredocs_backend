package main

import (
	"convert-to-pdf/config"
	"convert-to-pdf/database"
	"convert-to-pdf/redisstore"
	"convert-to-pdf/worker"
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"os"
	"os/signal"
	"syscall"
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
	r.GET("/healthz", func(c *gin.Context) {
		c.String(200, "ok")
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
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
