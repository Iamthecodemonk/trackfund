package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"
	"trackfund/internal/application/avatar"
	"trackfund/internal/composition"
	mono "trackfund/internal/infrastructure/mono"
	"trackfund/internal/infrastructure/postgres"
)

type avatarHTTPStore struct {
	put  []string
	fail bool
}

func (s *avatarHTTPStore) Put(_ context.Context, k string, b []byte) error {
	s.put = append(s.put, k)
	return nil
}
func (s *avatarHTTPStore) Delete(context.Context, string) error { return nil }
func (s *avatarHTTPStore) URL(k string) string                  { return "https://cdn.example/" + k }
func multipartRequest(t *testing.T, s *Server, token string, files int, kind string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for i := 0; i < files; i++ {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="file"; filename="avatar.png"`)
		header.Set("Content-Type", kind)
		part, err := mw.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(data)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/api/profile/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}
func TestAvatarAndAccountRemovalHTTP(t *testing.T) {
	app, pool, _, tokens := profileHTTPFixture(t)
	app.MonoRepo = postgres.NewMonoRepository(pool)
	app.MonoWebhooks = app.MonoRepo
	store := &avatarHTTPStore{}
	app.Avatar = &avatar.Service{Repo: postgres.NewProfileRepository(pool), Store: store}
	s := NewServer(app)
	token := tokens["alice"].AccessToken
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 20, 10)))
	if rr := multipartRequest(t, s, "", 1, "image/png", b.Bytes()); rr.Code != 401 {
		t.Fatal(rr.Code)
	}
	for _, tt := range []struct {
		files  int
		kind   string
		body   []byte
		status int
	}{{2, "image/png", b.Bytes(), 400}, {1, "image/gif", b.Bytes(), 415}, {1, "image/png", []byte("bad"), 400}, {1, "image/png", make([]byte, avatar.MaxSize+1), 413}} {
		rr := multipartRequest(t, s, token, tt.files, tt.kind, tt.body)
		if rr.Code != tt.status {
			t.Fatal("upload status", rr.Code, tt.status, rr.Body.String())
		}
	}
	rr := multipartRequest(t, s, token, 1, "image/png", b.Bytes())
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var uploaded struct{ Data struct{ PhotoURL string } }
	_ = json.Unmarshal(rr.Body.Bytes(), &uploaded)
	p := readProfile(t, profileRequest(s, "GET", "/api/profile/me", "", token))
	if p.PhotoURL == nil || *p.PhotoURL != uploaded.Data.PhotoURL {
		t.Fatal("upload URL not persisted")
	}
	bob := readProfile(t, profileRequest(s, "GET", "/api/profile/me", "", tokens["bob"].AccessToken))
	if bob.PhotoURL != nil {
		t.Fatal("avatar assigned to wrong user")
	}
	ctx := context.Background()
	meta := map[string]any{"meta": map[string]any{"data_status": "AVAILABLE"}}
	if err := app.MonoRepo.UpsertAccount(ctx, "internal", "alice", "bank", "mono", "Everyday", "0123456789", "058", "NGN", meta); err != nil {
		t.Fatal(err)
	}
	check := func(method, path, token string, want int) *httptest.ResponseRecorder {
		rr := profileRequest(s, method, path, "", token)
		if rr.Code != want {
			t.Fatalf("%s %s got %d wanted %d: %s", method, path, rr.Code, want, rr.Body.String())
		}
		return rr
	}
	check("GET", "/api/mono/accounts/internal/transactions", tokens["bob"].AccessToken, 404)
	check("DELETE", "/api/mono/accounts/internal", tokens["bob"].AccessToken, 404)
	check("GET", "/api/mono/accounts/internal/transactions", token, 202)
	if err := app.MonoRepo.MarkTransactionsSynced(ctx, "internal", time.Now()); err != nil {
		t.Fatal(err)
	}
	rr = check("GET", "/api/mono/accounts/internal/transactions", token, 200)
	if !strings.Contains(rr.Body.String(), `"data":[]`) {
		t.Fatal("empty state", rr.Body.String())
	}
	check("DELETE", "/api/mono/accounts/internal", "", 401)
	check("DELETE", "/api/mono/accounts/internal", token, 204)
	check("DELETE", "/api/mono/accounts/internal", token, 204)
	check("GET", "/api/mono/accounts/internal/transactions", token, 404)
	rr = check("GET", "/api/mono/accounts", token, 200)
	if !strings.Contains(rr.Body.String(), `"data":[]`) {
		t.Fatal("removed account listed")
	}
	// The endpoint is disabled without an operations secret, even for signed-in users.
	check("GET", "/internal/metrics", token, 404)
	app.Config.OperationsToken = strings.Repeat("o", 32)
	check("GET", "/internal/metrics", token, 401)
	req := httptest.NewRequest("GET", "/internal/metrics", nil)
	req.Header.Set("X-Operations-Token", app.Config.OperationsToken)
	metrics := httptest.NewRecorder()
	s.Handler().ServeHTTP(metrics, req)
	if metrics.Code != 200 || !strings.Contains(metrics.Body.String(), "trackiq_failed_unlinks") {
		t.Fatal(metrics.Code, metrics.Body.String())
	}
	app.Avatar = nil
	rr = multipartRequest(t, s, token, 1, "image/png", b.Bytes())
	if rr.Code != 503 {
		t.Fatal("unconfigured storage", rr.Code)
	}
}
func TestDocsMatchExportAndEndpoints(t *testing.T) {
	s := NewServer(composition.NewInMemoryApp())
	for _, path := range []string{"/openapi.json", "/api.json"} {
		rr := profileRequest(s, "GET", path, "", "")
		if rr.Code != 200 {
			t.Fatal(path, rr.Code)
		}
		var doc struct{ Paths map[string]json.RawMessage }
		if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		for _, endpoint := range []string{"/api/profile/me/avatar", "/api/mono/accounts/{id}"} {
			if _, ok := doc.Paths[endpoint]; !ok {
				t.Fatal("missing contract", endpoint)
			}
		}
	}
}
func TestProviderUnlinkIsSanitizedAndIdempotent(t *testing.T) {
	calls := 0
	c := mono.NewClient("https://api.withmono.com/v2", "provider-secret", "")
	status := 200
	c.HTTPClient = &http.Client{Transport: monoTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.withmono.com/v2/accounts/bank/unlink" || r.Method != "POST" || r.Header.Get("mono-sec-key") != "provider-secret" {
			t.Fatal("unlink request", r.Method, r.URL)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private provider response"))}, nil
	})}
	for _, code := range []int{200, 204, 404} {
		status = code
		if err := c.UnlinkAccount(context.Background(), "bank"); err != nil {
			t.Fatal(err)
		}
	}
	status = 500
	if err := c.UnlinkAccount(context.Background(), "bank"); err == nil || strings.Contains(err.Error(), "private provider") {
		t.Fatal("error not sanitized", err)
	}
	if calls != 4 {
		t.Fatal(calls)
	}
}
