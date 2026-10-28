package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	authapp "trackfund/internal/application/auth"
	profileapp "trackfund/internal/application/profile"
	"trackfund/internal/composition"
	domainauth "trackfund/internal/domain/auth"
	"trackfund/internal/infrastructure/postgres"
)

type profileMail struct{ email, code string }
type profileMailer struct {
	mu       sync.Mutex
	messages []profileMail
	fail     bool
}

func (m *profileMailer) SendOTP(_ context.Context, email, code string, _ domainauth.OTPType) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("private SMTP server details")
	}
	m.messages = append(m.messages, profileMail{email, code})
	return nil
}
func (m *profileMailer) last() profileMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.messages[len(m.messages)-1]
}
func profileHTTPFixture(t *testing.T) (*composition.App, *pgxpool.Pool, *profileMailer, map[string]authapp.AuthResult) {
	t.Helper()
	url := os.Getenv("TRACKFUND_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TRACKFUND_TEST_DATABASE_URL to an isolated PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("profile_http_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewAuthRepository(pool)
	mailer := &profileMailer{}
	secret := strings.Repeat("s", 48)
	app := composition.NewInMemoryApp()
	app.Auth = authapp.NewService(repo, mailer, authapp.Config{JWTSecret: secret, AccessTokenTTL: time.Hour})
	app.Profile = profileapp.NewService(postgres.NewProfileRepository(pool), mailer, secret)
	hash, err := (authapp.PasswordHasher{}).Hash("TestPassword123!")
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]authapp.AuthResult{}
	for _, id := range []string{"alice", "bob"} {
		user := domainauth.User{ID: id, FullName: "Original " + id, Email: id + "@example.com", PasswordHash: hash, Status: domainauth.StatusActive, IsEmailVerified: true, Role: domainauth.RoleUser, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err = repo.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
		result, err := app.Auth.Login(ctx, authapp.LoginCommand{Email: user.Email, Password: "TestPassword123!"})
		if err != nil {
			t.Fatal(err)
		}
		tokens[id] = result
	}
	return app, pool, mailer, tokens
}
func profileRequest(s *Server, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}
func readProfile(t *testing.T, rr *httptest.ResponseRecorder) profileapp.Profile {
	t.Helper()
	var result struct {
		Success bool
		Data    profileapp.Profile
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil || !result.Success {
		t.Fatalf("invalid profile response: status=%d", rr.Code)
	}
	return result.Data
}
func TestProfileHTTPReadsUpdatesAndValidates(t *testing.T) {
	app, pool, _, tokens := profileHTTPFixture(t)
	s := NewServer(app)
	token := tokens["alice"].AccessToken
	for _, route := range []struct{ method, path string }{{"GET", "/api/profile/me"}, {"PATCH", "/api/profile/me"}, {"POST", "/api/profile/me/email/verify"}} {
		for _, badToken := range []string{"", "invalid"} {
			rr := profileRequest(s, route.method, route.path, "{}", badToken)
			if rr.Code != 401 || !strings.Contains(rr.Body.String(), "unauthorized") {
				t.Fatalf("authentication: %d", rr.Code)
			}
		}
	}
	rr := profileRequest(s, "GET", "/api/profile/me?userId=bob", "", token)
	p := readProfile(t, rr)
	if p.ID != "alice" || p.Email != "alice@example.com" || p.PhotoURL != nil || p.PendingEmail != nil {
		t.Fatal("wrong profile", p.ID)
	}
	if rr.Header().Get("Cache-Control") != "no-store" || strings.Contains(rr.Body.String(), "PasswordHash") {
		t.Fatal("sensitive response")
	}
	rr = profileRequest(s, "PATCH", "/api/profile/me", `{"fullName":" Alice Updated ","phoneNumber":"+234 (801) 234-5678","photoUrl":"https://cdn.example.com/alice.png"}`, token)
	if rr.Code != 200 {
		t.Fatalf("patch: %d %s", rr.Code, rr.Body.String())
	}
	p = readProfile(t, rr)
	if p.FullName != "Alice Updated" || p.PhoneNumber != "+2348012345678" || p.PhotoURL == nil || p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		t.Fatal("invalid updated profile")
	}
	rr = profileRequest(s, "GET", "/api/profile/me", "", tokens["bob"].AccessToken)
	if readProfile(t, rr).FullName != "Original bob" {
		t.Fatal("updated another user")
	}
	for _, bad := range []string{`{"id":"bob","fullName":"Attack"}`, `{"role":"SUPER_ADMIN"}`, `{"fullName":null}`, `{"phoneNumber":"08012"}`, `{"photoUrl":"http://example.com/image.jpg"}`} {
		rr := profileRequest(s, "PATCH", "/api/profile/me", bad, token)
		if rr.Code != 422 || !strings.Contains(rr.Body.String(), "errors") {
			t.Fatalf("validation: %d", rr.Code)
		}
	}
	for _, bad := range []string{"null", "[]", "{", "{} {}"} {
		if rr := profileRequest(s, "PATCH", "/api/profile/me", bad, token); rr.Code != 400 {
			t.Fatalf("invalid JSON: %d", rr.Code)
		}
	}
	if rr := profileRequest(s, "PATCH", "/api/profile/me", `{"fullName":"`+strings.Repeat("x", 9000)+`"}`, token); rr.Code != 413 {
		t.Fatalf("request size: %d", rr.Code)
	}
	rr = profileRequest(s, "PATCH", "/api/profile/me", `{"phoneNumber":null,"photoUrl":null}`, token)
	p = readProfile(t, rr)
	if p.PhoneNumber != "" || p.PhotoURL != nil {
		t.Fatal("clearing failed")
	}
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM profile_audit_events WHERE user_id='alice'").Scan(&count); err != nil || count != 2 {
		t.Fatal("audit count", count, err)
	}
}
func TestProfileEmailMustBeVerifiedAndRevokesSessions(t *testing.T) {
	app, pool, mailer, tokens := profileHTTPFixture(t)
	s := NewServer(app)
	token := tokens["alice"].AccessToken
	staleUser, found, err := postgres.NewAuthRepository(pool).FindUserByID(context.Background(), "alice")
	if err != nil || !found {
		t.Fatal(err)
	}
	rr := profileRequest(s, "PATCH", "/api/profile/me", `{"email":"NEW@EXAMPLE.COM"}`, token)
	if rr.Code != 200 {
		t.Fatalf("request: %d %s", rr.Code, rr.Body.String())
	}
	p := readProfile(t, rr)
	if p.Email != "alice@example.com" || p.PendingEmail == nil || *p.PendingEmail != "new@example.com" || p.EmailChangeID == nil || p.EmailChangeExpiresAt == nil {
		t.Fatal("unverified email was activated")
	}
	sent := mailer.last()
	if sent.email != "new@example.com" || strings.Contains(rr.Body.String(), sent.code) {
		t.Fatal("code delivered incorrectly or exposed")
	}
	var storedHash string
	if err := pool.QueryRow(context.Background(), "SELECT code_hash FROM profile_email_changes WHERE user_id='alice'").Scan(&storedHash); err != nil || storedHash == sent.code {
		t.Fatal("code not hashed", err)
	}
	if _, err := app.Auth.Login(context.Background(), authapp.LoginCommand{Email: "new@example.com", Password: "TestPassword123!"}); !errors.Is(err, authapp.ErrInvalidCredentials) {
		t.Fatal("pending address can sign in", err)
	}
	body, _ := json.Marshal(map[string]string{"challengeId": *p.EmailChangeID, "code": sent.code})
	if rr := profileRequest(s, "POST", "/api/profile/me/email/verify", string(body), tokens["bob"].AccessToken); rr.Code != 422 {
		t.Fatalf("cross-user verification: %d", rr.Code)
	}
	rr = profileRequest(s, "POST", "/api/profile/me/email/verify", string(body), token)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "signInRequired") {
		t.Fatalf("verify: %d %s", rr.Code, rr.Body.String())
	}
	p = readProfile(t, rr)
	if p.Email != "new@example.com" || p.PendingEmail != nil {
		t.Fatal("verified email not applied")
	}
	if rr := profileRequest(s, "GET", "/api/profile/me", "", token); rr.Code != 401 {
		t.Fatal("old access token remains valid")
	}
	if _, err := app.Auth.Refresh(context.Background(), tokens["alice"].RefreshToken, "", "", ""); !errors.Is(err, authapp.ErrSessionExpired) {
		t.Fatal("old refresh token remains valid", err)
	}
	// Simulate a refresh already holding the old user snapshot when verification commits.
	staleSession, err := app.Auth.CreateTokensForUser(context.Background(), staleUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Auth.Refresh(context.Background(), staleSession.RefreshToken, "", "", ""); !errors.Is(err, authapp.ErrSessionExpired) {
		t.Fatal("in-flight refresh escaped revocation", err)
	}
	if _, err := app.Auth.Login(context.Background(), authapp.LoginCommand{Email: "alice@example.com", Password: "TestPassword123!"}); !errors.Is(err, authapp.ErrInvalidCredentials) {
		t.Fatal("old email still signs in", err)
	}
	if _, err := app.Auth.Login(context.Background(), authapp.LoginCommand{Email: "new@example.com", Password: "TestPassword123!"}); err != nil {
		t.Fatal("new email cannot sign in", err)
	}
	var count int
	pool.QueryRow(context.Background(), "SELECT count(*) FROM profile_audit_events WHERE action='profile.email.verified' AND user_id='alice'").Scan(&count)
	if count != 1 {
		t.Fatal("verification audit missing")
	}
}
func TestProfileConflictsMailFailureResendAndRateLimits(t *testing.T) {
	app, pool, mailer, tokens := profileHTTPFixture(t)
	s := NewServer(app)
	token := tokens["alice"].AccessToken
	if rr := profileRequest(s, "PATCH", "/api/profile/me", `{"email":"BOB@EXAMPLE.COM"}`, token); rr.Code != 409 {
		t.Fatalf("duplicate email: %d", rr.Code)
	}
	mailer.fail = true
	rr := profileRequest(s, "PATCH", "/api/profile/me", `{"email":"failed@example.com","fullName":"Saved Name"}`, token)
	if rr.Code != 503 || strings.Contains(rr.Body.String(), "private SMTP") {
		t.Fatalf("mail failure: %d", rr.Code)
	}
	p := readProfile(t, profileRequest(s, "GET", "/api/profile/me", "", token))
	if p.PendingEmail != nil || p.Email != "alice@example.com" || p.FullName != "Saved Name" {
		t.Fatal("mail failure state inconsistent")
	}
	mailer.fail = false
	rr = profileRequest(s, "PATCH", "/api/profile/me", `{"email":"next@example.com"}`, token)
	first := readProfile(t, rr)
	oldCode := mailer.last().code
	rr = profileRequest(s, "PATCH", "/api/profile/me", `{"email":"next@example.com"}`, token)
	second := readProfile(t, rr)
	if *first.EmailChangeID == *second.EmailChangeID {
		t.Fatal("resend did not replace challenge")
	}
	old, _ := json.Marshal(map[string]string{"challengeId": *first.EmailChangeID, "code": oldCode})
	if rr := profileRequest(s, "POST", "/api/profile/me/email/verify", string(old), token); rr.Code != 422 {
		t.Fatal("old code accepted")
	}
	// Sending the current email cancels the pending change without changing login.
	rr = profileRequest(s, "PATCH", "/api/profile/me", `{"email":"alice@example.com"}`, token)
	if readProfile(t, rr).PendingEmail != nil {
		t.Fatal("cancellation failed")
	}
	if _, err := pool.Exec(context.Background(), "UPDATE profile_rate_limits SET request_count=20 WHERE user_id='alice' AND operation='profile.patch'"); err != nil {
		t.Fatal(err)
	}
	rr = profileRequest(s, "PATCH", "/api/profile/me", `{"fullName":"Over Limit"}`, token)
	if rr.Code != 429 || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit: %d", rr.Code)
	}
}
