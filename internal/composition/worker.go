package composition

import (
	"context"
	"encoding/json"
	"log"
	"time"

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
		// fetch transactions and persist (best-effort)
		resp, err := app.MonoClient.GetTransactions(ctx, payload.MonoAccountID)
		if err != nil {
			return err
		}
		for _, tx := range resp.Data.Transactions {
			exists, err := app.MonoRepo.TransactionExists(ctx, tx.ID)
			if err != nil || exists {
				continue
			}
			internalID, err := app.MonoRepo.GetInternalAccountIDByMonoID(ctx, tx.AccountID)
			if err != nil || internalID == "" {
				continue
			}
			var paidAt time.Time
			if tx.PaidAt != "" {
				paidAt, _ = time.Parse(time.RFC3339, tx.PaidAt)
			}
			id, _ := authapp.RandomID()
			raw := map[string]any{
				"id":          tx.ID,
				"account_id":  tx.AccountID,
				"amount":      tx.Amount,
				"currency":    tx.Currency,
				"paid_at":     tx.PaidAt,
				"description": tx.Description,
			}
			_ = app.MonoRepo.InsertTransaction(ctx, id, internalID, tx.ID, tx.Amount, tx.Currency, paidAt, tx.Description, raw)
		}
		return nil
	})

	go func() {
		if err := srv.Run(mux); err != nil {
			log.Printf("asynq server stopped: %v", err)
		}
	}()

	return client, nil
}
