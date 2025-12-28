package main

import (
	"convert-to-pdf/config"
	"convert-to-pdf/database"
	"convert-to-pdf/routes"
	"fmt"
	"github.com/gin-gonic/gin"
	"os"
)

func main() {
	config.LoadConfig()
	database.Connect()
	database.Migrate()

	r := gin.Default()
	routes.SetupConvertToPdfRouter(r)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}
	if err := r.Run(fmt.Sprintf(":%s", port)); err != nil {
		panic(err)
	}
}
