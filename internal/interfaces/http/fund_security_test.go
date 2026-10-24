package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	authapp "trackfund/internal/application/auth"
	"trackfund/internal/composition"
	domainauth "trackfund/internal/domain/auth"
	"trackfund/internal/infrastructure/eventstore"
)

func TestFundHTTPAuthenticationAndOwnership(t *testing.T) {
	app := composition.NewInMemoryApp()
	repo := eventstore.NewInMemoryAuthRepository()
	secret := strings.Repeat("s", 48)
	app.Auth = authapp.NewService(repo, authapp.LogEmailSender{}, authapp.Config{JWTSecret: secret, AccessTokenTTL: time.Hour})
	manager := authapp.NewTokenManager(secret, "trackfund", time.Hour)
	tokens := map[string]string{}
	for _, id := range []string{"alice", "bob"} {
		if err := repo.CreateUser(context.Background(), domainauth.User{ID: id, Email: id + "@example.com", Status: domainauth.StatusActive, IsEmailVerified: true, Role: domainauth.RoleUser}); err != nil {
			t.Fatal(err)
		}
		token, err := manager.Generate(id, "USER", 0)
		if err != nil {
			t.Fatal(err)
		}
		tokens[id] = token
	}
	server := NewServer(app)
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rr := httptest.NewRecorder()
		server.Handler().ServeHTTP(rr, req)
		return rr
	}
	for _, token := range []string{"", "invalid", tokens["alice"] + "tampered"} {
		for _, route := range []struct{ method, path string }{{"POST", "/funds"}, {"GET", "/funds/anything"}, {"POST", "/funds/anything/contributions"}} {
			if rr := request(route.method, route.path, "{}", token); rr.Code != 401 {
				t.Fatalf("%s %s: %d", route.method, route.path, rr.Code)
			}
		}
	}
	rr := request("POST", "/funds", `{"name":"Rent","targetAmount":100,"currency":"NGN","userId":"bob","ownerId":"bob","UserID":"bob"}`, tokens["alice"])
	if rr.Code != 201 {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var result struct{ FundID string }
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	path := "/funds/" + result.FundID
	if rr := request("GET", path, "", tokens["bob"]); rr.Code != 403 {
		t.Fatalf("cross-user read: %d", rr.Code)
	}
	if rr := request("POST", path+"/contributions", `{"amount":10,"contributor":"Bob","userId":"alice"}`, tokens["bob"]); rr.Code != 403 {
		t.Fatalf("cross-user write: %d", rr.Code)
	}
	rr = request("GET", path, "", tokens["alice"])
	if rr.Code != 200 {
		t.Fatalf("owner read: %d", rr.Code)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("fund response can be cached")
	}
	var before struct{ TotalContributions int64 }
	json.Unmarshal(rr.Body.Bytes(), &before)
	if before.TotalContributions != 0 {
		t.Fatal("unauthorized contribution persisted")
	}
	if rr := request("POST", path+"/contributions", `{"amount":10,"contributor":"Alice"}`, tokens["alice"]); rr.Code != 202 {
		t.Fatalf("owner contribution: %d", rr.Code)
	}
	rr = request("GET", path, "", tokens["alice"])
	var after struct{ TotalContributions int64 }
	json.Unmarshal(rr.Body.Bytes(), &after)
	if after.TotalContributions != 10 {
		t.Fatal("owner contribution missing")
	}
	if rr := request("GET", "/funds/missing", "", tokens["alice"]); rr.Code != 404 {
		t.Fatalf("missing fund: %d", rr.Code)
	}
	if err := repo.UpdateTokenVersion(context.Background(), "alice", 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if rr := request("GET", path, "", tokens["alice"]); rr.Code != 401 {
		t.Fatalf("revoked token: %d", rr.Code)
	}
	expired, _ := authapp.NewTokenManager(secret, "trackfund", -time.Hour).Generate("bob", "USER", 0)
	if rr := request("GET", path, "", expired); rr.Code != 401 {
		t.Fatalf("expired token: %d", rr.Code)
	}
}
