package composition

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
	monoapp "trackfund/internal/application/mono"

	authapp "trackfund/internal/application/auth"

	"github.com/hibiken/asynq"
)

const TypeSyncAccount = "mono:sync_account"

// StartAsynqWorker starts an asynq server and returns a client for enqueuing tasks.
func StartAsynqWorker(ctx context.Context, redisAddr string, app *App) (*asynq.Client, error) {
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{Concurrency: 5},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeSyncAccount, func(ctx context.Context, t *asynq.Task) error {
		var payload struct {
			MonoAccountID string `json:"mono_account_id"`
		}
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			return err
		}
		return syncMonoAccount(ctx, app, payload.MonoAccountID)
	})

	go func() {
		if err := srv.Run(mux); err != nil {
			log.Printf("asynq server stopped: %v", err)
		}
	}()

	return client, nil
}

func syncMonoAccount(ctx context.Context, app *App, monoAccountID string) error {
	internalID, err := app.MonoRepo.GetInternalAccountIDByMonoID(ctx, monoAccountID)
	if err != nil {
		return err
	}
	account, found, err := app.MonoRepo.GetAccountByID(ctx, internalID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("account not found")
	}
	if !monoapp.TransactionsAvailable(account.Metadata) {
		return nil
	}
	syncStarted := time.Now().UTC()
	// Persist each transaction idempotently before recording successful sync.
	resp, err := app.MonoClient.GetTransactions(ctx, monoAccountID)
	if err != nil {
		return err
	}
	for _, tx := range resp.Data.Transactions {
		if tx.Currency == "" {
			tx.Currency = account.Currency
		}
		if tx.ID == "" || tx.Currency == "" {
			return fmt.Errorf("invalid transaction fields")
		}
		if tx.AccountID != "" && tx.AccountID != monoAccountID {
			return fmt.Errorf("transaction account mismatch")
		}
		var paidAt time.Time
		if tx.PaidAt != "" {
			paidAt, err = time.Parse(time.RFC3339, tx.PaidAt)
			if err != nil {
				return fmt.Errorf("invalid transaction timestamp")
			}
		}
		if paidAt.IsZero() {
			return fmt.Errorf("missing transaction timestamp")
		}
		id, err := authapp.RandomID()
		if err != nil {
			return err
		}
		raw := map[string]any{
			"id":          tx.ID,
			"account_id":  tx.AccountID,
			"amount":      tx.Amount,
			"currency":    tx.Currency,
			"paid_at":     tx.PaidAt,
			"description": tx.Description,
		}
		if err := app.MonoRepo.InsertTransaction(ctx, id, internalID, tx.ID, tx.Amount, tx.Currency, paidAt, tx.Description, raw); err != nil {
			return err
		}
	}
	return app.MonoRepo.MarkTransactionsSynced(ctx, internalID, syncStarted)
}
