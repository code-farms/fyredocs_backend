package main

import (
	"convert-from-pdf/config"
	"convert-from-pdf/database"
	"convert-from-pdf/routes"
	"fmt"
	"github.com/gin-gonic/gin"
	"os"
)

func main() {
	config.LoadConfig()
	database.Connect()
	database.Migrate()

	r := gin.Default()
	routes.SetupConvertFromPdfRouter(r)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}
	if err := r.Run(fmt.Sprintf(":%s", port)); err != nil {
		panic(err)
	}
}
