// Package objectstorage stores processed avatars using Cloudinary's signed Upload API.
package objectstorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"trackfund/internal/application/avatar"
	"trackfund/internal/config"
)

type Cloudinary struct {
	CloudName, APIKey, APISecret string
	Client                       *http.Client
	Now                          func() time.Time
}

func New(c config.Config) *Cloudinary {
	return &Cloudinary{CloudName: c.CloudinaryCloudName, APIKey: c.CloudinaryAPIKey, APISecret: c.CloudinaryAPISecret, Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Now: time.Now}
}
func (s *Cloudinary) URL(key string) string {
	return "https://res.cloudinary.com/" + s.CloudName + "/image/upload/v1/" + key
}
func signature(params url.Values, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+params.Get(k))
	}
	h := sha256.Sum256([]byte(strings.Join(pairs, "&") + secret))
	return hex.EncodeToString(h[:])
}
func publicID(key string) (string, error) {
	if !avatar.SafeKey(key) || !strings.HasSuffix(key, ".png") {
		return "", fmt.Errorf("invalid_object_key")
	}
	return strings.TrimSuffix(key, ".png"), nil
}
func (s *Cloudinary) request(ctx context.Context, operation, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.cloudinary.com/v1_1/"+s.CloudName+"/image/"+operation, body)
	if err != nil {
		return fmt.Errorf("invalid_storage_request")
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := s.Client.Do(req)
	if err != nil {
		return fmt.Errorf("cloudinary_request_failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("cloudinary_http_%d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return fmt.Errorf("invalid_cloudinary_response")
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid_cloudinary_response")
	}
	return nil
}
func (s *Cloudinary) Put(ctx context.Context, key string, png []byte) error {
	id, err := publicID(key)
	if err != nil {
		return err
	}
	params := url.Values{"public_id": {id}, "timestamp": {strconv.FormatInt(s.Now().Unix(), 10)}, "overwrite": {"false"}, "format": {"png"}}
	signed := signature(params, s.APISecret)
	params.Set("api_key", s.APIKey)
	params.Set("signature", signed)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, values := range params {
		if err = mw.WriteField(name, values[0]); err != nil {
			return fmt.Errorf("cloudinary_encode_failed")
		}
	}
	file, err := mw.CreateFormFile("file", "avatar.png")
	if err != nil {
		return fmt.Errorf("cloudinary_encode_failed")
	}
	if _, err = file.Write(png); err != nil {
		return fmt.Errorf("cloudinary_encode_failed")
	}
	if err = mw.Close(); err != nil {
		return fmt.Errorf("cloudinary_encode_failed")
	}
	var result struct {
		PublicID      string `json:"public_id"`
		ResourceType  string `json:"resource_type"`
		Format        string `json:"format"`
		Width, Height int
		Error         json.RawMessage `json:"error"`
	}
	if err = s.request(ctx, "upload", mw.FormDataContentType(), &body, &result); err != nil {
		return err
	}
	if result.PublicID != id || result.ResourceType != "image" || result.Format != "png" || result.Width != 512 || result.Height != 512 || len(result.Error) > 0 {
		return fmt.Errorf("invalid_cloudinary_upload_response")
	}
	return nil
}
func (s *Cloudinary) Delete(ctx context.Context, key string) error {
	id, err := publicID(key)
	if err != nil {
		return err
	}
	params := url.Values{"public_id": {id}, "timestamp": {strconv.FormatInt(s.Now().Unix(), 10)}, "invalidate": {"true"}}
	signed := signature(params, s.APISecret)
	params.Set("api_key", s.APIKey)
	params.Set("signature", signed)
	var result struct {
		Result string `json:"result"`
	}
	if err = s.request(ctx, "destroy", "application/x-www-form-urlencoded", strings.NewReader(params.Encode()), &result); err != nil {
		return err
	}
	if result.Result != "ok" && result.Result != "not found" {
		return fmt.Errorf("cloudinary_delete_failed")
	}
	return nil
}
