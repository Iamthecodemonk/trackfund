package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	monoapp "trackfund/internal/application/mono"
)

func webhookTestRepo(t *testing.T) *MonoRepository {
	t.Helper()
	url := os.Getenv("TRACKFUND_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TRACKFUND_TEST_DATABASE_URL to an isolated PostgreSQL test database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("mono_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, config)
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
	if err = Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, pool); err != nil {
		t.Fatal("repeat migration", err)
	}
	for _, user := range []string{"alice", "bob"} {
		if _, err = pool.Exec(ctx, `INSERT INTO users(id,full_name,email,password_hash,role,status,is_email_verified) VALUES ($1,$1,$1||'@example.com','test','USER','active',true)`, user); err != nil {
			t.Fatal(err)
		}
	}
	return NewMonoRepository(pool)
}
func ingestTest(t *testing.T, r *MonoRepository, id, typ string, seconds int, data string) {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"event_id":%q,"event":%q,"timestamp":"2026-10-26T09:00:%02dZ","data":%s}`, id, typ, seconds, data))
	e, err := monoapp.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.EnqueueWebhook(context.Background(), e, body); err != nil {
		t.Fatal(err)
	}
}
func processTest(t *testing.T, r *MonoRepository) {
	t.Helper()
	found, err := r.ProcessNextWebhook(context.Background())
	if err != nil || !found {
		t.Fatalf("process: found=%v err=%v", found, err)
	}
}
func countTest(t *testing.T, r *MonoRepository, table string) int {
	t.Helper()
	var count int
	if err := r.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func TestWebhookConnectionOrderIdempotencyAndReadiness(t *testing.T) {
	r := webhookTestRepo(t)
	ctx := context.Background()
	if err := r.CreateLinkSession(ctx, "session-a", "alice", "trackfund://mono/callback"); err != nil {
		t.Fatal(err)
	}
	update := `{"account":{"_id":"bank-a","name":"Alice","accountNumber":"12345","currency":"NGN","balance":9007199254740993,"institution":{"bankCode":"001"}},"meta":{"ref":"session-a","data_status":"AVAILABLE","retrieved_data":["transactions","balance"]}}`
	ingestTest(t, r, "updated", "mono.events.account_updated", 20, update)
	// Same delivery is accepted without inserting another inbox row.
	ingestTest(t, r, "updated", "mono.events.account_updated", 20, update)
	if countTest(t, r, "mono_webhook_events") != 1 {
		t.Fatal("delivery was duplicated")
	}
	processTest(t, r)
	ingestTest(t, r, "connected", "mono.events.account_connected", 10, `{"id":"bank-a","user_id":"bob","meta":{"ref":"session-a","data_status":"PROCESSING"}}`)
	processTest(t, r)
	status, found, err := r.GetLinkSession(ctx, "session-a", "alice")
	if err != nil || !found {
		t.Fatal(err)
	}
	if status.Status != "connected" || status.DataStatus != "AVAILABLE" || status.SyncStatus != "syncing" || status.TransactionsReady || status.AccountID == nil {
		t.Fatalf("bad status %+v", status)
	}
	if _, found, err := r.GetLinkSession(ctx, "session-a", "bob"); err != nil || found {
		t.Fatal("another user read session")
	}
	account, found, err := r.GetAccountByID(ctx, *status.AccountID)
	if err != nil || !found {
		t.Fatal(err)
	}
	if account.UserID != "alice" || account.AccountName != "Alice" || account.AccountNumber != "12345" || account.BankCode != "001" {
		t.Fatalf("bad mapping %+v", account)
	}
	if !strings.Contains(string(account.Metadata), "9007199254740993") {
		t.Fatal("integer balance lost precision")
	}
	if err := r.MarkTransactionsSynced(ctx, account.ID, time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	status, _, err = r.GetLinkSession(ctx, "session-a", "alice")
	if err != nil || !status.TransactionsReady || status.SyncStatus != "ready" {
		t.Fatalf("ready status %+v %v", status, err)
	}
	accounts, err := r.ListAccounts(ctx, "alice")
	if err != nil || len(accounts) != 1 || accounts[0].LastSyncedAt == nil {
		t.Fatal("account scan/sync marker", err)
	}
	empty, err := r.ListAccounts(ctx, "bob")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("new user must have an empty list")
	}
	// Reusing an event ID with a different payload is rejected.
	body := []byte(`{"event_id":"updated","event":"other.event","data":{}}`)
	e, err := monoapp.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.EnqueueWebhook(ctx, e, body); !errors.Is(err, monoapp.ErrEventConflict) {
		t.Fatal("event ID collision accepted", err)
	}
	// Partial balance-only data must not be treated as transaction-ready.
	ingestTest(t, r, "partial", "mono.events.account_updated", 30, `{"account":{"_id":"bank-a"},"meta":{"data_status":"PARTIAL","retrieved_data":["balance"]}}`)
	processTest(t, r)
	status, _, err = r.GetLinkSession(ctx, "session-a", "alice")
	if err != nil || status.TransactionsReady {
		t.Fatal("partial data treated as ready")
	}
	due, err := r.ListAllAccounts(ctx)
	if err != nil || len(due) != 0 {
		t.Fatal("not-ready account selected for sync", err)
	}
	ingestTest(t, r, "unlink", "mono.events.account_unlinked", 40, `{"account":{"id":"bank-a"}}`)
	processTest(t, r)
	status, _, err = r.GetLinkSession(ctx, "session-a", "alice")
	if err != nil || status.DataStatus != "DISCONNECTED" || status.SyncStatus != "unavailable" {
		t.Fatal("unlink state", status, err)
	}
}
func TestWebhookOwnershipCannotChange(t *testing.T) {
	r := webhookTestRepo(t)
	ctx := context.Background()
	for _, owner := range []string{"alice", "bob"} {
		if err := r.CreateLinkSession(ctx, owner, owner, "trackfund://mono/callback"); err != nil {
			t.Fatal(err)
		}
	}
	ingestTest(t, r, "a", "mono.events.account_connected", 1, `{"id":"bank-a","meta":{"ref":"alice","data_status":"PROCESSING"}}`)
	processTest(t, r)
	ingestTest(t, r, "b", "mono.events.account_connected", 2, `{"id":"bank-a","meta":{"ref":"bob","data_status":"AVAILABLE"}}`)
	processTest(t, r)
	var status, code string
	if err := r.pool.QueryRow(ctx, "SELECT status,last_error_code FROM mono_webhook_events WHERE event_id='b'").Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || code != "ownership_conflict" {
		t.Fatal("ownership event was not quarantined")
	}
	if err := r.UpsertAccount(ctx, "new", "bob", "bank-a", "mono", "Other", "1", "1", "NGN", map[string]any{}); !errors.Is(err, monoapp.ErrOwnershipConflict) {
		t.Fatal("SDK transferred ownership", err)
	}
	var owner string
	r.pool.QueryRow(ctx, "SELECT user_id FROM mono_accounts WHERE mono_account_id='bank-a'").Scan(&owner)
	if owner != "alice" {
		t.Fatal("ownership changed")
	}
	s, _, err := r.GetLinkSession(ctx, "bob", "bob")
	if err != nil || s.AccountID != nil || s.Status != "pending" {
		t.Fatal("conflicting session was linked", err)
	}
}
func TestWebhookRetriesRollbackTransactionsAndUseExactAmounts(t *testing.T) {
	r := webhookTestRepo(t)
	ctx := context.Background()
	if err := r.UpsertAccount(ctx, "internal-a", "alice", "bank-a", "mono", "Alice", "1", "1", "NGN", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	data := `{"transactions":[{"id":"tx-a","account_id":"bank-a","amount":9007199254740993,"currency":"NGN","paid_at":"2026-10-26T09:00:00Z"},{"id":"tx-b","account_id":"bank-b","amount":5,"currency":"NGN","paid_at":"2026-10-26T09:00:00Z"}]}`
	ingestTest(t, r, "tx-event", "mono.events.transactions_created", 1, data)
	processTest(t, r)
	if countTest(t, r, "mono_transactions") != 0 {
		t.Fatal("partial failed batch was committed")
	}
	var code string
	var attempts int
	r.pool.QueryRow(ctx, "SELECT last_error_code,attempts FROM mono_webhook_events WHERE event_id='tx-event'").Scan(&code, &attempts)
	if code != "mapping_pending" || attempts != 1 {
		t.Fatal("retry was not scheduled")
	}
	if err := r.UpsertAccount(ctx, "internal-b", "alice", "bank-b", "mono", "Alice", "2", "1", "NGN", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	r.pool.Exec(ctx, "UPDATE mono_webhook_events SET next_attempt_at=NOW() WHERE event_id='tx-event'")
	processTest(t, r)
	ingestTest(t, r, "tx-replay", "mono.events.transactions_created", 2, data)
	processTest(t, r)
	if countTest(t, r, "mono_transactions") != 2 {
		t.Fatal("transactions duplicated")
	}
	var amount int64
	r.pool.QueryRow(ctx, "SELECT amount FROM mono_transactions WHERE transaction_id='tx-a'").Scan(&amount)
	if amount != 9007199254740993 {
		t.Fatal("amount lost precision")
	}
	if err := r.InsertTransaction(ctx, "third", "internal-b", "tx-a", 1, "NGN", time.Now(), "", map[string]any{}); !errors.Is(err, monoapp.ErrOwnershipConflict) {
		t.Fatal("transaction account changed", err)
	}
	// Missing references survive a processing failure and become processable later.
	ingestTest(t, r, "before-session", "mono.events.account_connected", 3, `{"id":"bank-c","meta":{"ref":"later"}}`)
	processTest(t, r)
	if err := r.CreateLinkSession(ctx, "later", "bob", "trackfund://mono/callback"); err != nil {
		t.Fatal(err)
	}
	r.pool.Exec(ctx, "UPDATE mono_webhook_events SET next_attempt_at=NOW() WHERE event_id='before-session'")
	processTest(t, r)
	if countTest(t, r, "mono_accounts") != 3 {
		t.Fatal("out-of-order mapping did not recover")
	}
	// A locked event is skipped, then becomes eligible after rollback (crash recovery).
	ingestTest(t, r, "locked", "unknown.event", 4, `{}`)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "SELECT event_id FROM mono_webhook_events WHERE event_id='locked' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	if found, err := r.ProcessNextWebhook(ctx); err != nil || found {
		t.Fatal("worker did not skip locked event", err)
	}
	tx.Rollback(ctx)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := r.ProcessNextWebhook(ctx); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var state string
	r.pool.QueryRow(ctx, "SELECT status,attempts FROM mono_webhook_events WHERE event_id='locked'").Scan(&state, &attempts)
	if state != "processed" || attempts != 1 {
		t.Fatal("concurrent event processing duplicated work")
	}
	transactions, err := r.ListTransactionsByAccountID(ctx, "internal-a")
	if err != nil || len(transactions) != 1 {
		t.Fatal(err)
	}
	// Canonicalized JSON duplicate deliveries are equivalent despite formatting.
	var raw map[string]any
	json.Unmarshal([]byte(`{"event_id":"format","event":"unknown","data":{}}`), &raw)
	body, _ := json.MarshalIndent(raw, "", "  ")
	event, _ := monoapp.Decode(body)
	if err := r.EnqueueWebhook(ctx, event, body); err != nil {
		t.Fatal(err)
	}
	compact, _ := json.Marshal(raw)
	if err := r.EnqueueWebhook(ctx, event, compact); err != nil {
		t.Fatal("format-only duplicate rejected", err)
	}
}

func TestWebhookRetryExhaustionAndSingleAccountSession(t *testing.T) {
	r := webhookTestRepo(t)
	ctx := context.Background()
	ingestTest(t, r, "missing", "mono.events.account_connected", 1, `{"id":"bank","meta":{"ref":"missing-session"}}`)
	if _, err := r.pool.Exec(ctx, "UPDATE mono_webhook_events SET attempts=19 WHERE event_id='missing'"); err != nil {
		t.Fatal(err)
	}
	processTest(t, r)
	var state string
	var attempts int
	if err := r.pool.QueryRow(ctx, "SELECT status,attempts FROM mono_webhook_events WHERE event_id='missing'").Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || attempts != 20 {
		t.Fatal("retry limit was not enforced")
	}
	if err := r.CreateLinkSession(ctx, "ref", "alice", "trackfund://mono/callback"); err != nil {
		t.Fatal(err)
	}
	ingestTest(t, r, "first", "mono.events.account_connected", 2, `{"id":"bank-a","meta":{"ref":"ref"}}`)
	processTest(t, r)
	ingestTest(t, r, "second", "mono.events.account_connected", 3, `{"id":"bank-b","meta":{"ref":"ref"}}`)
	processTest(t, r)
	if countTest(t, r, "mono_accounts") != 1 {
		t.Fatal("session reused for another account")
	}
	ingestTest(t, r, "bad", "mono.events.account_updated", 4, `{}`)
	processTest(t, r)
	if err := r.pool.QueryRow(ctx, "SELECT status FROM mono_webhook_events WHERE event_id='bad'").Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "failed" {
		t.Fatal("malformed business payload was not quarantined")
	}
}
