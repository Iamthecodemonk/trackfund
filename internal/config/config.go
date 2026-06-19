package config

import "os"

type Config struct {
	Port      string
	JWTSecret string

	DatabaseURL string
	RedisAddr   string
	RedisPass   string
	RedisDB     int
}

func Load() Config {
	port := getenv("PORT", "3000")

	return Config{
		Port:        port,
		JWTSecret:   getenv("JWT_SECRET", "change-me-in-production"),
		DatabaseURL: getenv("DATABASE_URL", "postgres://trackfund:trackfund@localhost:5432/trackfund?sslmode=disable"),
		RedisAddr:   getenv("REDIS_ADDR", "localhost:6379"),
		RedisPass:   os.Getenv("REDIS_PASSWORD"),
		RedisDB:     0,
	}
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
