package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	authapp "trackfund/internal/application/auth"
	monoapp "trackfund/internal/application/mono"
	"trackfund/internal/composition"
	domainauth "trackfund/internal/domain/auth"
	"trackfund/internal/infrastructure/eventstore"
)

type fakeWebhookStore struct {
	calls           int
	err             error
	reference, user string
	status          monoapp.SessionStatus
}

func (f *fakeWebhookStore) EnqueueWebhook(_ context.Context, _ monoapp.Event, _ []byte) error {
	f.calls++
	return f.err
}
func (f *fakeWebhookStore) GetLinkSession(_ context.Context, ref, user string) (monoapp.SessionStatus, bool, error) {
	return f.status, ref == f.reference && user == f.user, f.err
}
func TestWebhookAcknowledgementAndSessionOwnership(t *testing.T) {
	app := composition.NewInMemoryApp()
	store := &fakeWebhookStore{reference: "ref-a", user: "alice", status: monoapp.SessionStatus{Reference: "ref-a", Status: "connected", SyncStatus: "syncing"}}
	app.MonoWebhooks = store
	app.Config.MonoWebhookSecret = "configured-secret"
	t.Setenv("MONO_WEBHOOK_SECRET", "different-environment-value") // Verification uses the loaded configuration.
	repo := eventstore.NewInMemoryAuthRepository()
	secret := strings.Repeat("s", 48)
	app.Auth = authapp.NewService(repo, authapp.LogEmailSender{}, authapp.Config{JWTSecret: secret, AccessTokenTTL: time.Hour})
	tokens := map[string]string{}
	for _, user := range []string{"alice", "bob"} {
		if err := repo.CreateUser(context.Background(), domainauth.User{ID: user, Email: user + "@example.com", Role: domainauth.RoleUser, Status: domainauth.StatusActive, IsEmailVerified: true}); err != nil {
			t.Fatal(err)
		}
		token, err := authapp.NewTokenManager(secret, "trackfund", time.Hour).Generate(user, "USER", 0)
		if err != nil {
			t.Fatal(err)
		}
		tokens[user] = token
	}
	server := NewServer(app)
	request := func(method, path, body, webhookSecret, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if webhookSecret != "" {
			req.Header.Set("mono-webhook-secret", webhookSecret)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rr := httptest.NewRecorder()
		server.Handler().ServeHTTP(rr, req)
		return rr
	}
	payload := `{"event_id":"evt","event":"mono.events.account_connected","timestamp":"2026-10-26T09:00:00Z","data":{"id":"bank","meta":{"ref":"ref-a"}}}`
	for _, key := range []string{"", "wrong-secret"} {
		if rr := request("POST", "/api/mono/webhook", payload, key, ""); rr.Code != 401 {
			t.Fatalf("bad secret: %d", rr.Code)
		}
	}
	if store.calls != 0 {
		t.Fatal("unsigned event was stored")
	}
	for _, body := range []string{"{", "{}", "[]", "null", payload + " {}"} {
		if rr := request("POST", "/api/mono/webhook", body, "configured-secret", ""); rr.Code != 400 {
			t.Fatalf("bad envelope: %d", rr.Code)
		}
	}
	if rr := request("POST", "/api/mono/webhook", strings.Repeat("x", 1024*1024+1), "configured-secret", ""); rr.Code != 413 {
		t.Fatalf("body limit: %d", rr.Code)
	}
	rr := request("POST", "/api/mono/webhook", payload, "configured-secret", "")
	if rr.Code != 200 || store.calls != 1 {
		t.Fatalf("persisted event not acknowledged: %d", rr.Code)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("webhook response cacheable")
	}
	store.err = errors.New("private database details")
	rr = request("POST", "/api/mono/webhook", payload, "configured-secret", "")
	if rr.Code != 503 || strings.Contains(rr.Body.String(), "private") {
		t.Fatal("storage failure acknowledged or exposed")
	}
	store.err = monoapp.ErrEventConflict
	if rr := request("POST", "/api/mono/webhook", payload, "configured-secret", ""); rr.Code != 409 {
		t.Fatal("event collision not rejected")
	}
	store.err = nil
	for _, user := range []string{"", "alice", "bob"} {
		want := 401
		if user == "alice" {
			want = 200
		}
		if user == "bob" {
			want = 404
		}
		rr := request("GET", "/api/mono/link-session/ref-a", "", "", tokens[user])
		if rr.Code != want {
			t.Fatalf("status for %s: %d want %d", user, rr.Code, want)
		}
		if user == "alice" && (!strings.Contains(rr.Body.String(), "syncing") || rr.Header().Get("Cache-Control") != "no-store") {
			t.Fatal("bad polling response")
		}
	}
	app.MonoWebhooks = nil
	if rr := request("POST", "/api/mono/webhook", payload, "configured-secret", ""); rr.Code != 503 {
		t.Fatal(fmt.Sprintf("missing storage: %d", rr.Code))
	}
}
