package composition

import (
	"context"
	"errors"
	"time"
)

func startMonoWebhookWorker(ctx context.Context, app *App) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		for i := 0; i < 25; i++ {
			if ctx.Err() != nil {
				return
			}
			workCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			found, err := app.MonoRepo.ProcessNextWebhook(workCtx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					app.Logger.Error("Mono webhook inbox temporarily unavailable", errors.New("storage_unavailable"))
				}
				break
			}
			if !found {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
