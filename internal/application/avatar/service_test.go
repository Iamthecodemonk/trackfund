package avatar

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
	"time"
)

type repoFake struct {
	reserve, commit, discard int
	commitErr                error
}

func (r *repoFake) Reserve(context.Context, string, string, string) error { r.reserve++; return nil }
func (r *repoFake) CommitAvatar(context.Context, string, string) error {
	r.commit++
	return r.commitErr
}
func (r *repoFake) Discard(context.Context, string) error { r.discard++; return nil }
func (r *repoFake) Allow(context.Context, string, string, int, time.Duration) (bool, error) {
	return true, nil
}

type storeFake struct {
	body []byte
	key  string
	fail bool
}

func (s *storeFake) Put(_ context.Context, key string, b []byte) error {
	s.key = key
	s.body = b
	if s.fail {
		return errors.New("private S3 details")
	}
	return nil
}
func (s *storeFake) Delete(context.Context, string) error { return nil }
func (s *storeFake) URL(key string) string                { return "https://cdn.example/" + key }
func TestDecodeCropAndStripMetadata(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.NRGBA{uint8(x % 255), 30, 80, 255})
		}
	}
	var p, j bytes.Buffer
	_ = png.Encode(&p, img)
	_ = jpeg.Encode(&j, img, nil)
	// A JPEG COM segment acts as sensitive metadata and must not survive re-encoding.
	meta := []byte("GPS PRIVATE LOCATION")
	jpegBody := append([]byte{0xff, 0xd8, 0xff, 0xfe, 0, byte(len(meta) + 2)}, meta...)
	jpegBody = append(jpegBody, j.Bytes()[2:]...)
	webp, err := os.ReadFile("testdata/sample.webp")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		mime string
		body []byte
	}{{"image/png", p.Bytes()}, {"image/jpeg", jpegBody}, {"image/webp", webp}} {
		repo := &repoFake{}
		store := &storeFake{}
		s := &Service{Repo: repo, Store: store}
		url, err := s.Upload(context.Background(), "alice", tt.mime, tt.body)
		if err != nil || repo.reserve != 1 || repo.commit != 1 || url == "" || !SafeKey(store.key) {
			t.Fatal(tt.mime, url, err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(store.body))
		if err != nil || cfg.Width != 512 || cfg.Height != 512 {
			t.Fatal("wrong derivative", cfg, err)
		}
		if bytes.Contains(store.body, meta) || bytes.Equal(store.body, tt.body) {
			t.Fatal("original bytes/metadata retained")
		}
	}
}
func TestImageValidationAndFailureCleanup(t *testing.T) {
	var valid bytes.Buffer
	_ = png.Encode(&valid, image.NewNRGBA(image.Rect(0, 0, 4, 4)))
	for _, tt := range []struct {
		mime   string
		body   []byte
		status int
	}{{"image/gif", valid.Bytes(), 415}, {"image/jpeg", valid.Bytes(), 415}, {"image/png", []byte("not an image"), 400}, {"image/png", make([]byte, MaxSize+1), 413}} {
		repo := &repoFake{}
		s := &Service{Repo: repo, Store: &storeFake{}}
		_, err := s.Upload(context.Background(), "alice", tt.mime, tt.body)
		e := IsInputError(err)
		if e == nil || e.Status != tt.status || repo.commit != 0 {
			t.Fatal(tt.status, err)
		}
	}
	for _, failure := range []string{"put", "commit"} {
		repo := &repoFake{}
		store := &storeFake{fail: failure == "put"}
		if failure == "commit" {
			repo.commitErr = errors.New("db unavailable")
		}
		_, err := (&Service{Repo: repo, Store: store}).Upload(context.Background(), "alice", "image/png", valid.Bytes())
		if err == nil || repo.discard != 1 {
			t.Fatal("failed upload not queued for cleanup", failure, err)
		}
	}
}
