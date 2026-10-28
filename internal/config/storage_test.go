package config

import (
	"strings"
	"testing"
)

func TestAvatarStorageValidation(t *testing.T) {
	c := validConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.AvatarStorageConfigured() {
		t.Fatal("empty credentials enabled storage")
	}
	c.CloudinaryCloudName = "trackiq-test"
	if err := c.Validate(); err == nil {
		t.Fatal("partial config accepted")
	}
	c.CloudinaryAPIKey = "example-key"
	c.CloudinaryAPISecret = "private-secret"
	if err := c.Validate(); err != nil || !c.AvatarStorageConfigured() {
		t.Fatal(err)
	}
	for _, bad := range []string{"https://cloudinary.example", "other/cloud", "example?x=1", "name\n"} {
		copy := c
		copy.CloudinaryCloudName = bad
		if err := copy.Validate(); err == nil {
			t.Fatal("unsafe cloud name accepted")
		}
	}
	for _, bad := range []string{" ", "key\n", " key "} {
		copy := c
		copy.CloudinaryAPISecret = bad
		if err := copy.Validate(); err == nil {
			t.Fatal("invalid secret accepted")
		}
	}
	c.OperationsToken = "short"
	if err := c.Validate(); err == nil {
		t.Fatal("short operations secret accepted")
	}
	c.OperationsToken = strings.Repeat("s", 32)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestCloudinaryEnvironment(t *testing.T) {
	t.Setenv("CLOUDINARY_CLOUD_NAME", "demo-test")
	t.Setenv("CLOUDINARY_API_KEY", "key")
	t.Setenv("CLOUDINARY_API_SECRET", "secret")
	c := Load()
	if c.CloudinaryCloudName != "demo-test" || c.CloudinaryAPIKey != "key" || c.CloudinaryAPISecret != "secret" || !c.AvatarStorageConfigured() {
		t.Fatal("Cloudinary settings not loaded")
	}
}
