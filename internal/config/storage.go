package config

import (
	"fmt"
	"regexp"
	"strings"
)

func (c Config) AvatarStorageConfigured() bool {
	return c.CloudinaryCloudName != "" && c.CloudinaryAPIKey != "" && c.CloudinaryAPISecret != ""
}
func (c Config) validateStorage() error {
	if c.OperationsToken != "" && (len(strings.TrimSpace(c.OperationsToken)) < 32 || strings.ContainsAny(c.OperationsToken, "\r\n")) {
		return fmt.Errorf("OPERATIONS_TOKEN must contain at least 32 bytes without newlines")
	}
	values := []string{c.CloudinaryCloudName, c.CloudinaryAPIKey, c.CloudinaryAPISecret}
	count := 0
	for _, v := range values {
		if v != "" {
			count++
		}
	}
	if count == 0 {
		return nil
	}
	if count != 3 {
		return fmt.Errorf("CLOUDINARY_CLOUD_NAME, CLOUDINARY_API_KEY and CLOUDINARY_API_SECRET are all required to enable avatar uploads")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(c.CloudinaryCloudName) {
		return fmt.Errorf("invalid CLOUDINARY_CLOUD_NAME")
	}
	for i, v := range values {
		if strings.TrimSpace(v) == "" || v != strings.TrimSpace(v) || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("Cloudinary setting %d must not be blank or contain surrounding whitespace or newlines", i+1)
		}
	}
	return nil
}
