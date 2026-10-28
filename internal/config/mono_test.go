package config

import (
	"strings"
	"testing"
)

func TestMonoDefaultsAndCallbacks(t *testing.T) {
	t.Setenv("MONO_BASE_URL", "")
	t.Setenv("MONO_SECRET_KEY", "")
	t.Setenv("MONO_ALLOWED_REDIRECT_URLS", " https://app.example.com/mono/callback,trackiq://mono/callback ")
	c := Load()
	if c.MonoBaseURL != "https://api.withmono.com/v2" {
		t.Fatal("incorrect API default")
	}
	if !c.AllowsMonoRedirect("https://app.example.com/mono/callback") || !c.AllowsMonoRedirect("trackiq://mono/callback") {
		t.Fatal("allowed callback rejected")
	}
	for _, bad := range []string{"javascript:alert(1)", "https://app.example.com.evil.test/mono/callback", "https://app.example.com/mono/callback?next=evil", "trackfund://evil/callback", "http://app.example.com/mono/callback"} {
		if c.AllowsMonoRedirect(bad) {
			t.Fatalf("accepted %s", bad)
		}
	}
}
func TestMonoConfigurationValidation(t *testing.T) {
	base := validConfig()
	base.MonoSecretKey = "secret"
	base.MonoBaseURL = "https://api.withmono.com/v2"
	base.MonoCallbackURL = "trackiq://mono/callback"
	base.MonoWebhookSecret = "webhook-secret"
	base.MonoTimeoutSecs = 30
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		modify func(*Config)
		field  string
	}{
		{"missing webhook", func(c *Config) { c.MonoWebhookSecret = "" }, "MONO_WEBHOOK_SECRET"},
		{"missing callback", func(c *Config) { c.MonoCallbackURL = "" }, "MONO_CALLBACK_URL"},
		{"insecure API", func(c *Config) { c.MonoBaseURL = "http://api.withmono.com/v2" }, "MONO_BASE_URL"},
		{"API credentials", func(c *Config) { c.MonoBaseURL = "https://name:password@api.withmono.com/v2" }, "MONO_BASE_URL"},
		{"timeout bounds", func(c *Config) { c.MonoTimeoutSecs = 121 }, "MONO_TIMEOUT_SECONDS"},
		{"callback injection", func(c *Config) { c.MonoCallbackURL = "javascript:alert(1)" }, "callbacks"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := base
			tt.modify(&c)
			if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("expected %s validation, got %v", tt.field, err)
			}
		})
	}
}

func TestMonoSeparateMobileAndWebCallbacks(t *testing.T) {
	t.Setenv("MONO_CALLBACK_URL", "")
	t.Setenv("MONO_ALLOWED_REDIRECT_URLS", "")
	t.Setenv("MONO_MOBILE_REDIRECT_URL", "trackiq://mono/callback")
	t.Setenv("MONO_WEB_REDIRECT_URL", "https://frontend.example.com/mono/callback")
	c := Load()
	for _, value := range []string{"trackiq://mono/callback", "https://frontend.example.com/mono/callback"} {
		if !c.AllowsMonoRedirect(value) {
			t.Fatalf("configured callback rejected: %s", value)
		}
	}
	for _, value := range []string{"trackfund://mono/callback", "https://trackfund//mono/callback", "trackiq://mono/callback?next=evil", "trackiq://evil/callback", "https://unconfigured.example.com/mono/callback"} {
		if c.AllowsMonoRedirect(value) {
			t.Fatalf("unexpected callback accepted: %s", value)
		}
	}
	c.MonoAllowedRedirectURLs = []string{"trackfund://mono/callback"}
	if c.AllowsMonoRedirect("trackfund://mono/callback") {
		t.Fatal("legacy scheme accepted via allow-list")
	}
	if c.validateMono() == nil {
		t.Fatal("legacy configured callback should fail startup")
	}
	c.MonoAllowedRedirectURLs = nil
	c.MonoWebRedirectURL = "trackiq://mono/callback"
	if c.validateMono() == nil {
		t.Fatal("web callback accepted mobile scheme")
	}
	c.MonoWebRedirectURL = "https://frontend.example.com/mono/callback"
	c.MonoMobileRedirectURL = "https://frontend.example.com/mono/callback"
	if c.validateMono() == nil {
		t.Fatal("mobile callback accepted web URL")
	}
}
