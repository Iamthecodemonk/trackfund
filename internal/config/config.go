package config

import (
	"fmt"
	"net/mail"
	"net/url"
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
	MonoPublicKey                                              string
	MonoSecretKey                                              string
	MonoBaseURL                                                string
	MonoCallbackURL                                            string
	MonoMobileRedirectURL                                      string
	MonoWebRedirectURL                                         string
	MonoAllowedRedirectURLs                                    []string
	MonoTimeoutSecs                                            int
	MonoWebhookSecret                                          string
	MonoDebugLogging                                           bool
	CloudinaryCloudName, CloudinaryAPIKey, CloudinaryAPISecret string
	OperationsToken                                            string
	// App runtime
	Env      string
	LogLevel string
}

func Load() Config {
	port := getenv("PORT", "3000")

	return Config{
		Port:                    port,
		CloudinaryCloudName:     strings.TrimSpace(os.Getenv("CLOUDINARY_CLOUD_NAME")),
		CloudinaryAPIKey:        os.Getenv("CLOUDINARY_API_KEY"),
		CloudinaryAPISecret:     os.Getenv("CLOUDINARY_API_SECRET"),
		OperationsToken:         os.Getenv("OPERATIONS_TOKEN"),
		JWTSecret:               os.Getenv("JWT_SECRET"),
		DatabaseURL:             getenv("DATABASE_URL", "postgres://trackfund:trackfund@localhost:5432/trackfund?sslmode=disable"),
		RedisAddr:               getenv("REDIS_ADDR", "localhost:6379"),
		RedisPass:               os.Getenv("REDIS_PASSWORD"),
		RedisDB:                 getenvInt("REDIS_DB", 0),
		JobsRedisAddr:           getenv("JOBS_REDIS_ADDR", getenv("REDIS_ADDR", "localhost:6379")),
		JobsRedisDB:             getenvInt("JOBS_REDIS_DB", getenvIntEnvFallback("REDIS_DB", 0)),
		SMTPHost:                os.Getenv("SMTP_HOST"),
		SMTPPort:                getenv("SMTP_PORT", "465"),
		SMTPUser:                os.Getenv("SMTP_USER"),
		SMTPPass:                os.Getenv("SMTP_PASS"),
		SMTPFrom:                os.Getenv("SMTP_FROM"),
		MonoPublicKey:           os.Getenv("MONO_PUBLIC_KEY"),
		MonoSecretKey:           os.Getenv("MONO_SECRET_KEY"),
		MonoBaseURL:             strings.TrimRight(getenv("MONO_BASE_URL", "https://api.withmono.com/v2"), "/"),
		MonoCallbackURL:         strings.TrimSpace(os.Getenv("MONO_CALLBACK_URL")),
		MonoMobileRedirectURL:   strings.TrimSpace(os.Getenv("MONO_MOBILE_REDIRECT_URL")),
		MonoWebRedirectURL:      strings.TrimSpace(os.Getenv("MONO_WEB_REDIRECT_URL")),
		MonoAllowedRedirectURLs: splitURLs(os.Getenv("MONO_ALLOWED_REDIRECT_URLS")),
		MonoTimeoutSecs:         getenvInt("MONO_TIMEOUT_SECONDS", 30),
		MonoWebhookSecret:       getenv("MONO_WEBHOOK_SECRET", ""),
		MonoDebugLogging:        getenvBool("MONO_DEBUG_LOGGING", false),
		SMTPUseTLS:              getenvBool("SMTP_USE_TLS", false),
		Env:                     getenv("ENV", "development"),
		LogLevel:                getenv("LOG_LEVEL", "info"),
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

// Validate rejects unsafe settings before opening network connections.
// Errors name configuration fields without exposing their values.
func (c Config) Validate() error {
	if err := c.validateStorage(); err != nil {
		return err
	}
	if err := c.validateMono(); err != nil {
		return err
	}
	if len(strings.TrimSpace(c.JWTSecret)) < 32 {
		return fmt.Errorf("JWT_SECRET must contain at least 32 non-padding bytes")
	}
	switch strings.ToLower(c.Env) {
	case "development", "test", "production":
	default:
		return fmt.Errorf("ENV must be development, test, or production")
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("PORT must be between 1 and 65535")
	}
	smtpConfigured := c.SMTPHost != "" || c.SMTPUser != "" || c.SMTPPass != "" || c.SMTPFrom != ""
	if smtpConfigured {
		for _, field := range []struct{ name, value string }{
			{"SMTP_HOST", c.SMTPHost}, {"SMTP_USER", c.SMTPUser},
			{"SMTP_PASS", c.SMTPPass}, {"SMTP_FROM", c.SMTPFrom},
		} {
			if strings.TrimSpace(field.value) == "" {
				return fmt.Errorf("%s is required when SMTP is configured", field.name)
			}
			if strings.ContainsAny(field.value, "\r\n") {
				return fmt.Errorf("%s must not contain line breaks", field.name)
			}
		}
		if _, err := mail.ParseAddress(c.SMTPFrom); err != nil {
			return fmt.Errorf("SMTP_FROM must be a valid email address")
		}
		smtpPort, err := strconv.Atoi(c.SMTPPort)
		if err != nil || smtpPort < 1 || smtpPort > 65535 {
			return fmt.Errorf("SMTP_PORT must be between 1 and 65535")
		}
	}
	if strings.EqualFold(c.Env, "production") {
		if !smtpConfigured {
			return fmt.Errorf("SMTP must be configured in production; development OTP logging is forbidden")
		}
		if c.MonoDebugLogging {
			return fmt.Errorf("MONO_DEBUG_LOGGING must be false in production")
		}
	}
	return nil
}

func splitURLs(raw string) []string {
	var urls []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			urls = append(urls, value)
		}
	}
	return urls
}

// ValidMonoRedirect accepts a web callback or the mobile app's registered deep link.
func ValidMonoRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" {
		return false
	}
	if raw == "trackiq://mono/callback" {
		return true
	}
	return u.Scheme == "https" && u.Hostname() != "" && u.Hostname() != "localhost" && !strings.ContainsAny(raw, "\r\n")
}

func (c Config) AllowsMonoRedirect(raw string) bool {
	if !ValidMonoRedirect(raw) {
		return false
	}
	if raw == c.MonoCallbackURL || raw == c.MonoMobileRedirectURL || raw == c.MonoWebRedirectURL {
		return true
	}
	for _, allowed := range c.MonoAllowedRedirectURLs {
		if raw == allowed {
			return true
		}
	}
	return false
}

func (c Config) validateMono() error {
	if c.MonoMobileRedirectURL != "" && c.MonoMobileRedirectURL != "trackiq://mono/callback" {
		return fmt.Errorf("MONO_MOBILE_REDIRECT_URL must be trackiq://mono/callback")
	}
	if c.MonoWebRedirectURL != "" && (!ValidMonoRedirect(c.MonoWebRedirectURL) || !strings.HasPrefix(c.MonoWebRedirectURL, "https://")) {
		return fmt.Errorf("MONO_WEB_REDIRECT_URL must be an HTTPS frontend callback without query/fragment")
	}
	for _, callback := range append(append([]string{}, c.MonoAllowedRedirectURLs...), c.MonoCallbackURL, c.MonoMobileRedirectURL, c.MonoWebRedirectURL) {
		if callback != "" && !ValidMonoRedirect(callback) {
			return fmt.Errorf("Mono callbacks must be HTTPS URLs without query/fragment or trackiq://mono/callback")
		}
	}
	if c.MonoSecretKey == "" {
		return nil
	}
	if strings.TrimSpace(c.MonoSecretKey) == "" || strings.ContainsAny(c.MonoSecretKey, "\r\n") {
		return fmt.Errorf("MONO_SECRET_KEY is invalid")
	}
	u, err := url.Parse(c.MonoBaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimRight(u.Path, "/") != "/v2" {
		return fmt.Errorf("MONO_BASE_URL must be an HTTPS v2 API URL without credentials, query or fragment")
	}
	if c.MonoTimeoutSecs < 1 || c.MonoTimeoutSecs > 120 {
		return fmt.Errorf("MONO_TIMEOUT_SECONDS must be between 1 and 120")
	}
	if strings.HasPrefix(c.MonoWebhookSecret, "https://") || strings.HasPrefix(c.MonoWebhookSecret, "http://") {
		return fmt.Errorf("MONO_WEBHOOK_SECRET must be the dashboard secret, not the webhook URL")
	}
	if strings.TrimSpace(c.MonoWebhookSecret) == "" {
		return fmt.Errorf("MONO_WEBHOOK_SECRET is required when Mono is configured")
	}
	if c.MonoCallbackURL == "" && c.MonoMobileRedirectURL == "" && c.MonoWebRedirectURL == "" && len(c.MonoAllowedRedirectURLs) == 0 {
		return fmt.Errorf("Set MONO_CALLBACK_URL, MONO_MOBILE_REDIRECT_URL, MONO_WEB_REDIRECT_URL or MONO_ALLOWED_REDIRECT_URLS when Mono is configured")
	}
	return nil
}
