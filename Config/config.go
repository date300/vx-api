package Config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func loadEnvFile(path string) map[string]string {
	values := make(map[string]string)
	file, err := os.Open(path)
	if err != nil {
		return values
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		values[key] = strings.Trim(value, `"'`)
	}
	return values
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	if values := loadEnvFile(".env"); values[key] != "" {
		return values[key]
	}
	return fallback
}

var (
	AppEnv       = getEnv("APP_ENV", "development")
	JWTSecret    = getEnv("JWT_SECRET", "vx_super_secret_key_2026")
	ServerHost   = getEnv("HOST", "0.0.0.0")
	ServerPort   = getEnv("PORT", "8080")
	BaseURL      = getEnv("BASE_URL", "http://192.168.0.140:8080")
	DatabaseDSN  = getDatabaseDSN()
	UploadDir    = "./public/uploads"
	SMTPHost     = getEnv("SMTP_HOST", "")
	SMTPPort     = getEnv("SMTP_PORT", "587")
	SMTPUsername = getEnv("SMTP_USERNAME", "")
	SMTPPassword = getEnv("SMTP_PASSWORD", "")
	SMTPFrom     = getEnv("SMTP_FROM", "")
	SMTPDisabled = getEnv("SMTP_DISABLED", "false") == "true" || AppEnv == "development"
	RedisHost    = getEnv("REDIS_HOST", "localhost")
	RedisPort    = getEnv("REDIS_PORT", "6379")
	RedisPass    = getEnv("REDIS_PASSWORD", "")
	CloudinaryURL = getEnv("CLOUDINARY_URL", "")
)

func getDatabaseDSN() string {
	if value := getEnv("DATABASE_URL", ""); value != "" {
		return value
	}

	host := getEnv("DB_HOST", "localhost")
	user := getEnv("DB_USER", "easywrqs_manage_data")
	password := getEnv("DB_PASSWORD", "123@456@789@0@")
	dbName := getEnv("DB_NAME", "easywrqs_app")
	port := getEnv("DB_PORT", "5432")
	sslMode := getEnv("DB_SSLMODE", "disable")

	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=Asia/Dhaka", host, user, password, dbName, port, sslMode)
}
