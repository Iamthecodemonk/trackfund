package objectstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"trackfund/internal/config"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func cloudinaryFixture() *Cloudinary {
	s := New(config.Config{CloudinaryCloudName: "trackiq-test", CloudinaryAPIKey: "example-key", CloudinaryAPISecret: "private-secret"})
	s.Now = func() time.Time { return time.Unix(173719931, 0) }
	return s
}
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestCloudinarySignedUpload(t *testing.T) {
	s := cloudinaryFixture()
	calls := 0
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.cloudinary.com/v1_1/trackiq-test/image/upload" || r.Method != "POST" {
			t.Fatal(r.Method, r.URL)
		}
		if err := r.ParseMultipartForm(2 << 20); err != nil {
			t.Fatal(err)
		}
		defer r.MultipartForm.RemoveAll()
		for name, want := range map[string]string{"public_id": "profile/alice/new", "timestamp": "173719931", "overwrite": "false", "format": "png", "api_key": "example-key"} {
			if r.FormValue(name) != want {
				t.Fatal("incorrect upload field", name)
			}
		}
		signed := sha256.Sum256([]byte("format=png&overwrite=false&public_id=profile/alice/new&timestamp=173719931private-secret"))
		if r.FormValue("signature") != hex.EncodeToString(signed[:]) {
			t.Fatal("upload signature mismatch")
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		raw, _ := io.ReadAll(file)
		if string(raw) != "processed PNG" {
			t.Fatal("wrong upload bytes")
		}
		if r.FormValue("api_secret") != "" {
			t.Fatal("raw API secret sent")
		}
		return response(200, `{"public_id":"profile/alice/new","resource_type":"image","format":"png","width":512,"height":512,"secure_url":"https://res.cloudinary.com/trackiq-test/image/upload/v123/profile/alice/new.png"}`), nil
	})}
	if err := s.Put(context.Background(), "profile/alice/new.png", []byte("processed PNG")); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || s.URL("profile/alice/new.png") != "https://res.cloudinary.com/trackiq-test/image/upload/v1/profile/alice/new.png" {
		t.Fatal("invalid delivery URL")
	}
}
func TestCloudinaryDeleteAndResponseValidation(t *testing.T) {
	s := cloudinaryFixture()
	calls := 0
	result := "ok"
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.cloudinary.com/v1_1/trackiq-test/image/destroy" {
			t.Fatal(r.URL)
		}
		_ = r.ParseForm()
		if r.FormValue("invalidate") != "true" || r.FormValue("public_id") != "profile/alice/new" {
			t.Fatal("invalid destroy parameters")
		}
		signed := sha256.Sum256([]byte("invalidate=true&public_id=profile/alice/new&timestamp=173719931private-secret"))
		if r.FormValue("signature") != hex.EncodeToString(signed[:]) {
			t.Fatal("destroy signature mismatch")
		}
		return response(200, `{"result":"`+result+`"}`), nil
	})}
	for _, value := range []string{"ok", "not found"} {
		result = value
		if err := s.Delete(context.Background(), "profile/alice/new.png"); err != nil {
			t.Fatal(err)
		}
	}
	result = "failed"
	if err := s.Delete(context.Background(), "profile/alice/new.png"); err == nil {
		t.Fatal("failed destruction accepted")
	}
	if err := s.Delete(context.Background(), "../../another-user"); err == nil || calls != 3 {
		t.Fatal("unsafe deletion requested")
	}
	for _, body := range []string{`{}`, `{"public_id":"someone-else","resource_type":"image","format":"png","width":512,"height":512}`, `{"public_id":"profile/alice/new","resource_type":"video","format":"png","width":512,"height":512}`, strings.Repeat("x", 65537)} {
		s.Client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return response(200, body), nil })}
		if err := s.Put(context.Background(), "profile/alice/new.png", nil); err == nil {
			t.Fatal("invalid provider response accepted")
		}
	}
	s.Client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return response(401, "PRIVATE PROVIDER DETAILS"), nil })}
	if err := s.Put(context.Background(), "profile/alice/new.png", nil); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("provider error not sanitized", err)
	}
}
func TestCloudinarySignatureUsesRawSortedValues(t *testing.T) {
	params := url.Values{"timestamp": {"1315060510"}, "public_id": {"sample_image"}, "eager": {"w_400,h_300,c_pad|w_260,h_200,c_crop"}}
	expected := sha256.Sum256([]byte("eager=w_400,h_300,c_pad|w_260,h_200,c_crop&public_id=sample_image&timestamp=1315060510abcd"))
	if signature(params, "abcd") != hex.EncodeToString(expected[:]) {
		t.Fatal("canonical parameter signing mismatch")
	}
}
