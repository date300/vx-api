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

	fmt.Println("PostgreSQL Connected ✅")

	
}
