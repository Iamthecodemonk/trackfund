package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	profileapp "trackfund/internal/application/profile"
)

func TestProfileRepositoryIsolationAndAtomicUpdate(t *testing.T) {
	fixture := webhookTestRepo(t)
	r := NewProfileRepository(fixture.pool)
	ctx := context.Background()
	alice, err := r.Get(ctx, "alice")
	if err != nil || alice.ID != "alice" || alice.PhotoURL != nil {
		t.Fatal(err)
	}
	name := "Updated Alice"
	phone := "+2348012345678"
	photo := "https://cdn.example.com/alice.jpg"
	c := profileapp.Changes{FullName: profileapp.OptionalString{Set: true, Value: &name}, PhoneNumber: profileapp.OptionalString{Set: true, Value: &phone}, PhotoURL: profileapp.OptionalString{Set: true, Value: &photo}}
	result, err := r.Update(ctx, "alice", c, nil)
	if err != nil || result.FullName != name || result.PhoneNumber != phone || result.PhotoURL == nil {
		t.Fatal("profile did not persist", err)
	}
	bob, err := r.Get(ctx, "bob")
	if err != nil || bob.FullName == name {
		t.Fatal("modified another user", err)
	}
	if _, err := r.Get(ctx, "missing"); !errors.Is(err, profileapp.ErrNotFound) {
		t.Fatal("missing user", err)
	}
	taken := "BOB@EXAMPLE.COM"
	c.Email = profileapp.OptionalString{Set: true, Value: &taken}
	another := "Must Roll Back"
	c.FullName.Value = &another
	if _, err := r.Update(ctx, "alice", c, &profileapp.Challenge{ID: strings.Repeat("a", 32), Email: taken, CodeHash: "hash", ExpiresAt: time.Now().Add(time.Minute)}); !errors.Is(err, profileapp.ErrEmailConflict) {
		t.Fatal("duplicate email accepted", err)
	}
	result, _ = r.Get(ctx, "alice")
	if result.FullName != name {
		t.Fatal("partial update persisted on email conflict")
	}
	var count int
	fixture.pool.QueryRow(ctx, "SELECT count(*) FROM profile_audit_events WHERE user_id='alice'").Scan(&count)
	if count != 1 {
		t.Fatal("incorrect audit count")
	}
	cleared, err := r.Update(ctx, "alice", profileapp.Changes{PhotoURL: profileapp.OptionalString{Set: true}, PhoneNumber: profileapp.OptionalString{Set: true}}, nil)
	if err != nil || cleared.PhotoURL != nil || cleared.PhoneNumber != "" {
		t.Fatal("clear fields", err)
	}
	for i := 0; i < 3; i++ {
		ok, err := r.Allow(ctx, "alice", "test", 2, time.Hour)
		if err != nil || ok != (i < 2) {
			t.Fatal("rate limit", i, ok, err)
		}
	}
	if _, err := fixture.pool.Exec(ctx, "UPDATE profile_rate_limits SET window_start=NOW()-INTERVAL '2 hours' WHERE operation='test'"); err != nil {
		t.Fatal(err)
	}
	if ok, err := r.Allow(ctx, "alice", "test", 2, time.Hour); err != nil || !ok {
		t.Fatal("window did not reset", err)
	}
}
func TestEmailVerificationAttemptsExpiryAndRace(t *testing.T) {
	fixture := webhookTestRepo(t)
	r := NewProfileRepository(fixture.pool)
	ctx := context.Background()
	email := "new@example.com"
	id := strings.Repeat("b", 32)
	hash := strings.Repeat("c", 64)
	c := profileapp.Changes{Email: profileapp.OptionalString{Set: true, Value: &email}}
	challenge := &profileapp.Challenge{ID: id, Email: email, CodeHash: hash, ExpiresAt: time.Now().Add(time.Minute)}
	request := func() {
		t.Helper()
		if _, err := r.Update(ctx, "alice", c, challenge); err != nil {
			t.Fatal(err)
		}
	}
	request()
	if _, err := r.Verify(ctx, "bob", id, hash); !errors.Is(err, profileapp.ErrInvalidCode) {
		t.Fatal("another user verified code", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := r.Verify(ctx, "alice", id, "wrong"); !errors.Is(err, profileapp.ErrInvalidCode) {
			t.Fatal("wrong code", err)
		}
	}
	if _, err := r.Verify(ctx, "alice", id, hash); !errors.Is(err, profileapp.ErrAttemptsExhausted) {
		t.Fatal("attempt limit bypassed", err)
	}
	challenge.ID = strings.Repeat("d", 32)
	challenge.ExpiresAt = time.Now().Add(-time.Minute)
	request()
	if _, err := r.Verify(ctx, "alice", challenge.ID, hash); !errors.Is(err, profileapp.ErrInvalidCode) {
		t.Fatal("expired code accepted", err)
	}
	challenge.ID = strings.Repeat("e", 32)
	challenge.ExpiresAt = time.Now().Add(time.Minute)
	request()
	// A newer request invalidates the old challenge and cannot be cleared by it.
	if err := r.CancelChallenge(ctx, "alice", id); err != nil {
		t.Fatal(err)
	}
	p, err := r.Get(ctx, "alice")
	if err != nil || p.EmailChangeID == nil || *p.EmailChangeID != challenge.ID {
		t.Fatal("stale cancellation removed new request", err)
	}
	if _, err := fixture.pool.Exec(ctx, "UPDATE users SET email=$1 WHERE id='bob'", email); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Verify(ctx, "alice", challenge.ID, hash); !errors.Is(err, profileapp.ErrEmailConflict) {
		t.Fatal("email race conflict missing", err)
	}
	p, _ = r.Get(ctx, "alice")
	if p.Email != "alice@example.com" || p.PendingEmail == nil {
		t.Fatal("email changed on conflict")
	}
	if _, err := fixture.pool.Exec(ctx, "UPDATE users SET email='bob@example.com' WHERE id='bob'"); err != nil {
		t.Fatal(err)
	}
	p, err = r.Verify(ctx, "alice", challenge.ID, hash)
	if err != nil || p.Email != email || p.PendingEmail != nil {
		t.Fatal("valid verification failed", err)
	}
	if _, err := r.Verify(ctx, "alice", challenge.ID, hash); !errors.Is(err, profileapp.ErrInvalidCode) {
		t.Fatal("verification replay accepted", err)
	}
}

func TestConcurrentProfileEmailClaimsAndRateLimit(t *testing.T) {
	fixture := webhookTestRepo(t)
	r := NewProfileRepository(fixture.pool)
	ctx := context.Background()
	email := "shared@example.com"
	digest := strings.Repeat("a", 64)
	for _, user := range []string{"alice", "bob"} {
		id := strings.Repeat("a", 32)
		if user == "bob" {
			id = strings.Repeat("b", 32)
		}
		if _, err := r.Update(ctx, user, profileapp.Changes{Email: profileapp.OptionalString{Set: true, Value: &email}}, &profileapp.Challenge{ID: id, Email: email, CodeHash: digest, ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, user := range []string{"alice", "bob"} {
		wg.Add(1)
		go func(user string) {
			defer wg.Done()
			id := strings.Repeat("a", 32)
			if user == "bob" {
				id = strings.Repeat("b", 32)
			}
			_, err := r.Verify(ctx, user, id, digest)
			results <- err
		}(user)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, profileapp.ErrEmailConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent email ownership was not unique")
	}
	allowedResults := make(chan bool, 20)
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, err := r.Allow(ctx, "alice", "concurrent", 2, time.Hour)
			allowedResults <- allowed
			errs <- err
		}()
	}
	wg.Wait()
	close(allowedResults)
	close(errs)
	count := 0
	for allowed := range allowedResults {
		if allowed {
			count++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count != 2 {
		t.Fatalf("rate limit admitted %d requests", count)
	}
}
