package composition

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
	mono "trackfund/internal/infrastructure/mono"
	"trackfund/internal/infrastructure/postgres"
)

type syncTransport func(*http.Request) (*http.Response, error)

func (f syncTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMonoSyncGatesReadinessAndPersistsV2Transactions(t *testing.T) {
	url := os.Getenv("TRACKFUND_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("mono_sync_%d", time.Now().UnixNano())
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
	t.Cleanup(func() { pool.Close(); admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); admin.Close() })
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,full_name,email,password_hash,role,status) VALUES ('alice','Alice','alice@example.com','test','USER','active')`); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewMonoRepository(pool)
	metadata := map[string]any{"meta": map[string]any{"data_status": "PROCESSING"}}
	if err = repo.UpsertAccount(ctx, "internal", "alice", "bank", "mono", "Alice", "123", "001", "NGN", metadata); err != nil {
		t.Fatal(err)
	}
	app := NewInMemoryApp()
	app.MonoRepo = repo
	app.MonoWebhooks = repo
	app.MonoClient = mono.NewClient("https://api.withmono.com/v2", "secret", "")
	calls := 0
	body := `{"data":[{"id":"tx","amount":9007199254740993,"date":"2026-10-26T09:00:00Z","narration":"Transfer"}],"meta":{"next":null}}`
	app.MonoClient.HTTPClient = &http.Client{Transport: syncTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if err := syncMonoAccount(ctx, app, "bank"); err != nil || calls != 0 {
		t.Fatal("fetched data before readiness", err)
	}
	metadata["meta"] = map[string]any{"data_status": "AVAILABLE", "retrieved_data": []string{"transactions"}}
	if err = repo.UpsertAccount(ctx, "unused", "alice", "bank", "mono", "Alice", "123", "001", "NGN", metadata); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := syncMonoAccount(ctx, app, "bank"); err != nil {
			t.Fatal(err)
		}
	}
	account, found, err := repo.GetAccountByID(ctx, "internal")
	if err != nil || !found || account.LastSyncedAt == nil {
		t.Fatal("sync marker missing", err)
	}
	transactions, err := repo.ListTransactionsByAccountID(ctx, "internal")
	if err != nil || len(transactions) != 1 || transactions[0].Amount != 9007199254740993 || transactions[0].Currency != "NGN" || transactions[0].Description != "Transfer" {
		t.Fatal("v2 sync failed or duplicated", err)
	}
	// A malformed provider response leaves the account syncing, never falsely empty.
	if _, err = pool.Exec(ctx, "UPDATE mono_accounts SET last_synced_at=NULL WHERE id='internal'"); err != nil {
		t.Fatal(err)
	}
	body = `{"data":{}}`
	if err := syncMonoAccount(ctx, app, "bank"); err == nil {
		t.Fatal("malformed provider response treated as success")
	}
	account, _, err = repo.GetAccountByID(ctx, "internal")
	if err != nil || account.LastSyncedAt != nil {
		t.Fatal("failed sync updated success marker")
	}
}
