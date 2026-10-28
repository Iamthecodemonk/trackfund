package avatar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	"image/png"
	"mime"
	"strings"
	"time"
	authapp "trackfund/internal/application/auth"
)

const MaxSize = 5 * 1024 * 1024

var slots = make(chan struct{}, 2)

type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return e.Code }

type Store interface {
	Put(context.Context, string, []byte) error
	Delete(context.Context, string) error
	URL(string) string
}
type Repository interface {
	Reserve(context.Context, string, string, string) error
	CommitAvatar(context.Context, string, string) error
	Discard(context.Context, string) error
	Allow(context.Context, string, string, int, time.Duration) (bool, error)
}
type Service struct {
	Repo  Repository
	Store Store
}

func (s *Service) Upload(ctx context.Context, userID, contentType string, body []byte) (string, error) {
	if len(body) > MaxSize {
		return "", &Error{413, "avatar_too_large"}
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", &Error{415, "unsupported_image_type"}
	}
	formats := map[string]string{"image/jpeg": "jpeg", "image/png": "png", "image/webp": "webp"}
	expected, ok := formats[mt]
	if !ok {
		return "", &Error{415, "unsupported_image_type"}
	}
	allowed, err := s.Repo.Allow(ctx, userID, "profile.avatar", 10, time.Hour)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", &Error{429, "avatar_rate_limited"}
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return "", &Error{400, "invalid_image"}
	}
	if format != expected {
		return "", &Error{415, "image_type_mismatch"}
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 12000000 {
		return "", &Error{400, "image_dimensions_exceeded"}
	}
	src, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return "", &Error{400, "invalid_image"}
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	x, y := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	output := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	draw.CatmullRom.Scale(output, output.Bounds(), src, image.Rect(x, y, x+side, y+side), draw.Src, nil)
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, output); err != nil {
		return "", err
	}
	random, err := authapp.RandomID()
	if err != nil {
		return "", err
	}
	owner := sha256.Sum256([]byte(userID))
	key := "profile/" + hex.EncodeToString(owner[:16]) + "/" + random + ".png"
	url := s.Store.URL(key)
	// Reserve before upload: failures or process crashes leave a durable cleanup row.
	if err = s.Repo.Reserve(ctx, userID, key, url); err != nil {
		return "", err
	}
	if err = s.Store.Put(ctx, key, encoded.Bytes()); err != nil {
		_ = s.Repo.Discard(ctx, key)
		return "", fmt.Errorf("avatar_storage_failed")
	}
	if err = s.Repo.CommitAvatar(ctx, userID, key); err != nil {
		_ = s.Repo.Discard(ctx, key)
		return "", err
	}
	return url, nil
}
func IsInputError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}
func SafeKey(key string) bool {
	if !strings.HasPrefix(key, "profile/") || strings.Contains(key, "..") {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '/' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}
