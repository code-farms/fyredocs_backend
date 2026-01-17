package database

import (
	"log"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func Connect() {
	var err error
	dsn := os.Getenv("DATABASE_URL")
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database")
	}

	log.Println("Database connection established")
}

func Migrate() {
	DB.AutoMigrate(&User{}, &AuthMetadata{}, &SubscriptionPlan{}, &ProcessingJob{}, &FileMetadata{})
	log.Println("Database migration completed")
}
