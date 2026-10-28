package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
	monoapp "trackfund/internal/application/mono"
	profileapp "trackfund/internal/application/profile"
)

func TestRemovalTombstoneAndDurableUnlink(t *testing.T) {
	r := webhookTestRepo(t)
	ctx := context.Background()
	if err := r.CreateLinkSession(ctx, "ref", "alice", "trackiq://mono/callback"); err != nil {
		t.Fatal(err)
	}
	ingestTest(t, r, "connect", "mono.events.account_updated", 1, `{"account":{"id":"bank","name":"Everyday","institution":{"name":"Test Bank","bankCode":"058"}},"meta":{"ref":"ref","data_status":"AVAILABLE"}}`)
	processTest(t, r)
	accounts, err := r.ListAccounts(ctx, "alice")
	if err != nil || len(accounts) != 1 {
		t.Fatal(accounts, err)
	}
	a := accounts[0]
	if a.BankName != "Test Bank" || a.BankLogoURL != nil {
		t.Fatal("branding", a)
	}
	if err = r.InsertTransaction(ctx, "tx", ""+a.ID, "tx", 500000, "NGN", time.Now(), "Debit", map[string]any{"type": "debit"}); err != nil {
		t.Fatal(err)
	}
	txs, err := r.ListTransactionsByAccountID(ctx, a.ID)
	if err != nil || len(txs) != 1 || txs[0].Amount != -500000 || txs[0].Raw != nil {
		t.Fatal(txs, err)
	}
	if ok, err := r.DeleteAccount(ctx, a.ID, "bob"); ok || err != nil {
		t.Fatal("foreign delete", ok, err)
	}
	for i := 0; i < 2; i++ {
		if ok, err := r.DeleteAccount(ctx, a.ID, "alice"); !ok || err != nil {
			t.Fatal("owner delete", ok, err)
		}
	}
	if got, err := r.ListAccounts(ctx, "alice"); err != nil || len(got) != 0 || got == nil {
		t.Fatal("removed link listed", got, err)
	}
	if _, found, err := r.GetAccountByID(ctx, a.ID); err != nil || found {
		t.Fatal("removed link visible")
	}
	if got, err := r.ListTransactionsByAccountID(ctx, a.ID); err != nil || len(got) != 0 {
		t.Fatal("removed transactions visible", err)
	}
	if err = r.InsertTransaction(ctx, "late", a.ID, "late", 1, "NGN", time.Now(), "late", nil); err != nil {
		t.Fatal("late write should be harmless", err)
	}
	ingestTest(t, r, "late-update", "mono.events.account_updated", 2, `{"account":{"id":"bank"},"meta":{"ref":"ref","data_status":"AVAILABLE"}}`)
	processTest(t, r)
	ingestTest(t, r, "late-tx", "mono.events.transactions_created", 3, `{"transactions":[{"id":"late-webhook","account_id":"bank","amount":1,"currency":"NGN","paid_at":"2026-10-28T00:00:00Z"}]}`)
	processTest(t, r)
	if n := countTest(t, r, "mono_transactions"); n != 1 {
		t.Fatal("late writes imported", n)
	}
	status, _, err := r.GetLinkSession(ctx, "ref", "alice")
	if err != nil || status.Status != "failed" || status.AccountID != nil || status.TransactionsReady {
		t.Fatal("removed session revived", status, err)
	}
	if err := r.UpsertAccount(ctx, "new", "alice", "bank", "mono", "name", "123", "058", "NGN", nil); !errors.Is(err, monoapp.ErrOwnershipConflict) {
		t.Fatal("tombstone revived", err)
	}
	if ok, err := r.ProcessUnlink(ctx, func(context.Context, string) error { return errors.New("private provider detail") }); !ok || err != nil {
		t.Fatal(err)
	}
	var code string
	var attempts int
	if err = r.pool.QueryRow(ctx, "SELECT last_error_code,attempts FROM mono_unlink_jobs WHERE mono_account_id='bank'").Scan(&code, &attempts); err != nil || code != "provider_unlink_failed" || attempts != 1 {
		t.Fatal(code, attempts, err)
	}
	_, _ = r.pool.Exec(ctx, "UPDATE mono_unlink_jobs SET next_attempt_at=NOW()")
	if ok, err := r.ProcessUnlink(ctx, func(_ context.Context, id string) error {
		if id != "bank" {
			t.Fatal(id)
		}
		return nil
	}); !ok || err != nil {
		t.Fatal(err)
	}
	if ok, err := r.ProcessUnlink(ctx, func(context.Context, string) error { t.Fatal("processed twice"); return nil }); ok || err != nil {
		t.Fatal(ok, err)
	}
}
func TestDeleteWaitsForInFlightWrites(t *testing.T) {
	r := webhookTestRepo(t)
	ctx := context.Background()
	if err := r.UpsertAccount(ctx, "id", "alice", "bank", "mono", "a", "1", "058", "NGN", nil); err != nil {
		t.Fatal(err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM mono_accounts WHERE id='id' FOR SHARE"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := r.DeleteAccount(ctx, "id", "alice"); done <- err }()
	select {
	case err := <-done:
		t.Fatal("delete passed write lock", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = r.InsertTransaction(ctx, "late", "id", "late", 1, "NGN", time.Now(), "", nil); err != nil {
		t.Fatal(err)
	}
	if countTest(t, r, "mono_transactions") != 0 {
		t.Fatal("write after removal")
	}
}
func TestAvatarCommitCleanupAndConcurrentReplacement(t *testing.T) {
	r := webhookTestRepo(t)
	repo := NewProfileRepository(r.pool)
	ctx := context.Background()
	for _, key := range []string{"profile/a/first.png", "profile/a/second.png"} {
		if err := repo.Reserve(ctx, "alice", key, "https://cdn.example/"+key); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for _, key := range []string{"profile/a/first.png", "profile/a/second.png"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			if err := repo.CommitAvatar(ctx, "alice", key); err != nil {
				t.Error(err)
			}
		}(key)
	}
	wg.Wait()
	profile, err := repo.Get(ctx, "alice")
	if err != nil || profile.PhotoURL == nil {
		t.Fatal(profile, err)
	}
	var active int
	_ = r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM profile_avatar_objects WHERE status='active'").Scan(&active)
	if active != 1 {
		t.Fatal("concurrent uploads left multiple active objects", active)
	}
	_, _ = r.pool.Exec(ctx, "UPDATE profile_avatar_objects SET delete_after=NOW()-INTERVAL '1 second'")
	called := ""
	if ok, err := repo.CleanupAvatar(ctx, func(_ context.Context, key string) error { called = key; return errors.New("temporary storage error") }); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if called == "" || "https://cdn.example/"+called == *profile.PhotoURL {
		t.Fatal("active image deleted")
	}
	_, _ = r.pool.Exec(ctx, "UPDATE profile_avatar_objects SET delete_after=NOW()-INTERVAL '1 second' WHERE status='garbage'")
	if ok, err := repo.CleanupAvatar(ctx, func(context.Context, string) error { return nil }); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := repo.CleanupAvatar(ctx, func(context.Context, string) error { t.Fatal("active image deleted"); return nil }); ok || err != nil {
		t.Fatal(ok, err)
	}
	// Clearing photoUrl schedules cleanup only after the profile transaction succeeds.
	_, err = repo.Update(ctx, "alice", profileapp.Changes{PhotoURL: profileapp.OptionalString{Set: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	_ = r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM profile_avatar_objects WHERE status='garbage'").Scan(&count)
	if count != 1 {
		t.Fatal("cleared image not queued")
	}
}
func TestBankSnapshotNotExposed(t *testing.T) {
	a := MonoAccount{Metadata: json.RawMessage(`{"secret":"provider credential"}`)}
	a.applyBranding()
	b, _ := json.Marshal(a)
	if string(b) == "" {
		t.Fatal("empty account")
	}
	var obj map[string]any
	_ = json.Unmarshal(b, &obj)
	if _, ok := obj["metadata"]; ok {
		t.Fatal("provider snapshot exposed")
	}
}
