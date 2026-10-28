package composition

import (
	"context"
	"errors"
	"log/slog"
	"time"
	"trackfund/internal/infrastructure/postgres"
)

func startMaintenance(ctx context.Context, app *App, profile *postgres.ProfileRepository) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	monitor := time.NewTicker(time.Minute)
	defer monitor.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if app.MonoClient != nil && app.MonoClient.SecretKey != "" {
				for i := 0; i < 5; i++ {
					found, err := app.MonoRepo.ProcessUnlink(ctx, app.MonoClient.UnlinkAccount)
					if err != nil {
						if ctx.Err() == nil {
							app.Logger.Error("Mono unlink queue unavailable", errors.New("storage_unavailable"))
						}
						break
					}
					if !found {
						break
					}
				}
			}
			if app.Avatar != nil {
				for i := 0; i < 5; i++ {
					found, err := profile.CleanupAvatar(ctx, app.Avatar.Store.Delete)
					if err != nil || !found {
						break
					}
				}
			}
		case <-monitor.C:
			counts, err := app.MonoRepo.OperationalCounts(ctx)
			if err == nil && (counts.FailedWebhooks+counts.PendingSessions+counts.FailedUnlinks+counts.AvatarCleanupFailures) > 0 {
				app.Logger.Info("operational backlog", slog.Int64("failed_webhooks", counts.FailedWebhooks), slog.Int64("stale_pending_sessions", counts.PendingSessions), slog.Int64("failed_unlinks", counts.FailedUnlinks), slog.Int64("avatar_cleanup_failures", counts.AvatarCleanupFailures))
			}
		}
	}
}
