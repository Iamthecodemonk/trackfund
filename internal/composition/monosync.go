package composition

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

// startMonoSync enqueues sync tasks for all linked accounts at the given interval.
func startMonoSync(ctx context.Context, app *App, client *asynq.Client, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			accounts, err := app.MonoRepo.ListAllAccounts(ctx)
			if err != nil {
				app.Logger.Error("list mono accounts failed", err)
				continue
			}
			for _, acc := range accounts {
				payload, err := json.Marshal(map[string]string{"mono_account_id": acc.MonoAccountID})
				if err != nil {
					app.Logger.Error("marshal mono sync payload failed", err, slog.String("account_id", acc.MonoAccountID))
					continue
				}

				task := asynq.NewTask(TypeSyncAccount, payload)

				_, err = client.EnqueueContext(ctx, task, asynq.MaxRetry(3), asynq.Unique(5*time.Minute), asynq.ProcessIn(1*time.Second))
				if err != nil {
					if errors.Is(err, asynq.ErrDuplicateTask) {
						continue
					}
					app.Logger.Error("enqueue mono sync task failed", err, slog.String("account_id", acc.MonoAccountID))
					continue
				}
			}
		}
	}
}
