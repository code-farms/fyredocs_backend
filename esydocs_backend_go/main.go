package main

import (
	"esydocs_backend_go/config"
	"esydocs_backend_go/database"
	"esydocs_backend_go/routes"
	"github.com/gin-gonic/gin"
)

func main() {
	// Load environment variables
	config.LoadConfig()

	// Connect to the database
	database.Connect()

	// Run migrations
	database.Migrate()

	r := gin.Default()
	
	// Setup routes
	routes.SetupRouter(r)

	r.Run() // listen and serve on 0.0.0.0:8080
}
