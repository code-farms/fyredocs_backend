package main

import (
	"upload-service/config"
	"upload-service/routes"
	"fmt"
	"github.com/gin-gonic/gin"
	"os"
)

func main() {
	config.LoadConfig()

	r := gin.Default()
	routes.SetupUploadRouter(r)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	if err := r.Run(fmt.Sprintf(":%s", port)); err != nil {
		panic(err)
	}
}
