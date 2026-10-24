package config

import (
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{JWTSecret: strings.Repeat("s", 48), Port: "3000", Env: "development", SMTPPort: "465"}
}
func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"development without SMTP", func(c *Config) {}, ""},
		{"missing JWT", func(c *Config) { c.JWTSecret = "" }, "JWT_SECRET"},
		{"short JWT", func(c *Config) { c.JWTSecret = "short" }, "JWT_SECRET"},
		{"padded JWT", func(c *Config) { c.JWTSecret = strings.Repeat(" ", 32) }, "JWT_SECRET"},
		{"invalid environment", func(c *Config) { c.Env = "prod" }, "ENV"},
		{"invalid port", func(c *Config) { c.Port = "0" }, "PORT"},
		{"partial SMTP", func(c *Config) { c.SMTPHost = "smtp.example.com" }, "SMTP_USER"},
		{"production requires SMTP", func(c *Config) { c.Env = "production" }, "SMTP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.change(&c)
			err := c.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %s error, got %v", tt.want, err)
			}
			if c.JWTSecret != "" && strings.Contains(err.Error(), c.JWTSecret) {
				t.Fatal("error exposed secret")
			}
		})
	}
}
func TestProductionSMTP(t *testing.T) {
	c := validConfig()
	c.Env = "production"
	c.SMTPHost = "smtp.example.com"
	c.SMTPUser = "user"
	c.SMTPPass = "private-password"
	c.SMTPFrom = "noreply@example.com"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.MonoDebugLogging = true
	if err := c.Validate(); err == nil {
		t.Fatal("production accepted Mono debug logging")
	}
	c.MonoDebugLogging = false
	c.SMTPFrom = "bad\r\nBcc: other@example.com"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted mail header injection")
	}
	c.SMTPFrom = "noreply@example.com"
	c.SMTPPort = "invalid"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted invalid SMTP port")
	}
}
func TestLoadHasNoCredentialFallbacks(t *testing.T) {
	for _, key := range []string{"JWT_SECRET", "SMTP_HOST", "SMTP_USER", "SMTP_PASS", "SMTP_FROM", "MONO_DEBUG_LOGGING"} {
		t.Setenv(key, "")
	}
	c := Load()
	if c.JWTSecret != "" || c.SMTPHost != "" || c.SMTPUser != "" || c.SMTPPass != "" || c.SMTPFrom != "" {
		t.Fatal("loaded fallback credentials")
	}
	if c.MonoDebugLogging {
		t.Fatal("Mono debug logging enabled by default")
	}
}
