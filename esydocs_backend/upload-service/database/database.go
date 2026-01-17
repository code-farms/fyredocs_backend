package database

import (
	"context"
	"log"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func Connect() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database")
	}

	sqlDB, err := DB.DB()
	if err != nil {
		log.Fatal("Failed to create database handle")
	}
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		log.Fatal("Database ping failed")
	}

	log.Println("Database connection established")
}

func Migrate() {
	if err := DB.AutoMigrate(
		&User{},
		&AuthMetadata{},
		&SubscriptionPlan{},
		&ProcessingJob{},
		&FileMetadata{},
	); err != nil {
		log.Fatalf("Database migration failed: %v", err)
	}
	log.Println("Database migration completed")
}
