package Config

import (
	"fmt"
	"log"

	

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() {
	dsn := DatabaseDSN
	if dsn == "" {
		log.Fatal("database DSN is empty")
	}

	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("DB Connection Error: ", err)
	}

	sqlDB, err := DB.DB()
	if err != nil {
		log.Fatal("Failed to get generic database object: ", err)
	}

	// Connection Pool settings optimized for Neon
	// Neon suggests keeping connections fresh and not holding too many idle ones
	// if using their connection pooler (endpoint with -pooler).
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(0) // No limit on lifetime

	fmt.Println("PostgreSQL Connected ✅")
}
