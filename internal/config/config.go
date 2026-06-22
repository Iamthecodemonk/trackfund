package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port      string
	JWTSecret string

	DatabaseURL string
	RedisAddr   string
	RedisPass   string
	RedisDB     int
	// Jobs Redis (optional, defaults to RedisAddr)
	JobsRedisAddr string
	JobsRedisDB   int
	// SMTP settings for sending emails
	SMTPHost   string
	SMTPPort   string
	SMTPUser   string
	SMTPPass   string
	SMTPFrom   string
	SMTPUseTLS bool
	// Mono (open banking) config
	MonoPublicKey     string
	MonoSecretKey     string
	MonoBaseURL       string
	MonoWebhookSecret string
	// App runtime
	Env      string
	LogLevel string
}

func Load() Config {
	port := getenv("PORT", "3000")

	return Config{
		Port:              port,
		JWTSecret:         getenv("JWT_SECRET", "change-me-in-production"),
		DatabaseURL:       getenv("DATABASE_URL", "postgres://trackfund:trackfund@localhost:5432/trackfund?sslmode=disable"),
		RedisAddr:         getenv("REDIS_ADDR", "localhost:6379"),
		RedisPass:         os.Getenv("REDIS_PASSWORD"),
		RedisDB:           getenvInt("REDIS_DB", 0),
		JobsRedisAddr:     getenv("JOBS_REDIS_ADDR", getenv("REDIS_ADDR", "localhost:6379")),
		JobsRedisDB:       getenvInt("JOBS_REDIS_DB", getenvIntEnvFallback("REDIS_DB", 0)),
		SMTPHost:          getenv("SMTP_HOST", "mail.privateemail.com"),
		SMTPPort:          getenv("SMTP_PORT", "465"),
		SMTPUser:          getenv("SMTP_USER", "noreply@skills4export.com"),
		SMTPPass:          getenv("SMTP_PASS", "!123skills4exporT"),
		SMTPFrom:          getenv("SMTP_FROM", "noreply@skills4export.com"),
		MonoPublicKey:     getenv("MONO_PUBLIC_KEY", "test_pk_t7abdmtip2x92wradvyn"),
		MonoSecretKey:     getenv("MONO_SECRET_KEY", "test_sk_x2zlus1mprtutchfo449"),
		MonoBaseURL:       getenv("MONO_BASE_URL", "https://api.withmono.com/v2"),
		MonoWebhookSecret: getenv("MONO_WEBHOOK_SECRET", ""),
		SMTPUseTLS:        getenvBool("SMTP_USE_TLS", false),
		Env:               getenv("ENV", "development"),
		LogLevel:          getenv("LOG_LEVEL", "info"),
	}
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getenvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// getenvIntEnvFallback reads key or falls back to another env int
func getenvIntEnvFallback(key string, fallback int) int {
	return getenvInt(key, fallback)
}

func getenvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	default:
		return fallback
	}
}
