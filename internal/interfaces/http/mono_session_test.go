package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	authapp "trackfund/internal/application/auth"
	"trackfund/internal/composition"
	domainauth "trackfund/internal/domain/auth"
	"trackfund/internal/infrastructure/eventstore"
	mono "trackfund/internal/infrastructure/mono"
)

type sessionRecord struct{ ref, user, redirect string }
type fakeMonoSessions struct {
	records []sessionRecord
	err     error
}

func (s *fakeMonoSessions) CreateLinkSession(_ context.Context, ref, user, redirect string) error {
	if s.err != nil {
		return s.err
	}
	s.records = append(s.records, sessionRecord{ref, user, redirect})
	return nil
}

type monoTransport func(*http.Request) (*http.Response, error)

func (f monoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMonoSessionUsesAuthenticatedCustomerAndPersistsReference(t *testing.T) {
	app := composition.NewInMemoryApp()
	repo := eventstore.NewInMemoryAuthRepository()
	secret := strings.Repeat("s", 48)
	app.Auth = authapp.NewService(repo, authapp.LogEmailSender{}, authapp.Config{JWTSecret: secret, AccessTokenTTL: time.Hour})
	user := domainauth.User{ID: "alice", FullName: "Alice Test", Email: "alice@example.com", Status: domainauth.StatusActive, IsEmailVerified: true, Role: domainauth.RoleUser}
	if err := repo.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	token, err := authapp.NewTokenManager(secret, "trackfund", time.Hour).Generate("alice", "USER", 0)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeMonoSessions{}
	app.MonoSessions = store
	app.Config.MonoCallbackURL = "trackfund://mono/callback"
	app.Config.MonoAllowedRedirectURLs = []string{"https://app.example.com/mono/callback"}
	app.MonoClient = mono.NewClient("https://api.withmono.com/v2/", "provider-secret", "")
	calls := 0
	providerStatus := 200
	providerBody := `{"data":{"mono_url":"https://link.mono.co/test"}}`
	app.MonoClient.HTTPClient = &http.Client{Transport: monoTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.withmono.com/v2/accounts/initiate" || r.Method != "POST" {
			t.Fatalf("wrong endpoint: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("mono-sec-key") != "provider-secret" {
			t.Fatal("missing provider authentication")
		}
		var payload struct {
			Customer struct{ Name, Email string }
			Scope    string
			Meta     struct{ Ref string }
			Redirect string `json:"redirect_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Customer.Name != user.FullName || payload.Customer.Email != user.Email || payload.Scope != "auth" {
			t.Fatal("client spoofed provider customer or scope")
		}
		saved := store.records[len(store.records)-1]
		if saved.ref != payload.Meta.Ref || saved.user != "alice" || saved.redirect != payload.Redirect || len(saved.ref) < 24 {
			t.Fatal("reference not persisted before provider call")
		}
		return &http.Response{StatusCode: providerStatus, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(providerBody))}, nil
	})}
	server := NewServer(app)
	request := func(body, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/mono/link-session", strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		rr := httptest.NewRecorder()
		server.Handler().ServeHTTP(rr, req)
		return rr
	}
	if rr := request("{}", ""); rr.Code != 401 {
		t.Fatalf("unauthenticated: %d", rr.Code)
	}
	if calls != 0 || len(store.records) != 0 {
		t.Fatal("unauthenticated request created session")
	}
	body := `{"redirectUrl":"https://app.example.com/mono/callback","customer":{"name":"Mallory","email":"evil@example.com"},"scope":"payments","user_id":"mallory","meta":{"ref":"attacker"}}`
	rr := request(body, token)
	if rr.Code != 200 {
		t.Fatalf("session: %d %s", rr.Code, rr.Body.String())
	}
	var result struct {
		Success bool
		Data    struct {
			MonoURL           string `json:"mono_url"`
			Reference, Status string
		}
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.Data.Reference != store.records[0].ref || result.Data.Status != "pending" || result.Data.MonoURL == "" {
		t.Fatal("incomplete success response")
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("link credential may be cached")
	}
	rr = request("", token)
	if rr.Code != 200 || store.records[1].redirect != "trackfund://mono/callback" || store.records[0].ref == store.records[1].ref {
		t.Fatal("fallback or unique reference failed")
	}
	for _, bad := range []string{`{"redirect_url":"https://evil.example/callback"}`, `{"redirect_url":"javascript:alert(1)"}`, `{"redirect_url":"trackfund://mono/callback","callbackUrl":"https://app.example.com/mono/callback"}`} {
		before := calls
		rr := request(bad, token)
		if rr.Code != 422 || calls != before {
			t.Fatalf("invalid callback contacted provider: %d", rr.Code)
		}
	}
	for _, bad := range []string{"{", "[]", "{} {}"} {
		if rr := request(bad, token); rr.Code != 400 {
			t.Fatalf("malformed body: %d", rr.Code)
		}
	}
	if rr := request(`{"padding":"`+strings.Repeat("x", 5000)+`"}`, token); rr.Code != 413 {
		t.Fatalf("oversized body: %d", rr.Code)
	}
	store.err = errors.New("private database detail")
	before := calls
	rr = request("{}", token)
	if rr.Code != 503 || calls != before || strings.Contains(rr.Body.String(), "private") {
		t.Fatal("storage failure was unsafe")
	}
	store.err = nil
	providerStatus = 401
	providerBody = `{"message":"provider-secret private details"}`
	rr = request("{}", token)
	if rr.Code != 502 || strings.Contains(rr.Body.String(), "provider-secret") {
		t.Fatal("provider failure was exposed or treated as user auth failure")
	}
	providerStatus = 200
	providerBody = `{"data":{"client_token":"legacy-token"}}`
	rr = request("{}", token)
	if rr.Code != 502 {
		t.Fatal("token-only response accepted for hosted flow")
	}
	providerBody = `{"data":{"mono_url":"javascript:alert(1)"}}`
	rr = request("{}", token)
	if rr.Code != 502 {
		t.Fatal("unsafe provider URL accepted")
	}
}
