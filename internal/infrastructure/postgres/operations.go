package postgres

import (
	"context"
)

type OperationalCounts struct{ FailedWebhooks, PendingSessions, FailedSessions, FailedUnlinks, AvatarCleanupFailures int64 }

func (r *MonoRepository) OperationalCounts(ctx context.Context) (OperationalCounts, error) {
	var c OperationalCounts
	err := r.pool.QueryRow(ctx,
		"SELECT (SELECT COUNT(*) FROM mono_webhook_events WHERE status='failed'),(SELECT COUNT(*) FROM mono_link_sessions WHERE status='pending' AND created_at<NOW()-INTERVAL '15 minutes'),(SELECT COUNT(*) FROM mono_link_sessions WHERE status='failed'),(SELECT COUNT(*) FROM mono_unlink_jobs WHERE status='failed'),(SELECT COUNT(*) FROM profile_avatar_objects WHERE status<>'active' AND attempts>0)").Scan(&c.FailedWebhooks, &c.PendingSessions, &c.FailedSessions, &c.FailedUnlinks, &c.AvatarCleanupFailures)
	return c, err
}
