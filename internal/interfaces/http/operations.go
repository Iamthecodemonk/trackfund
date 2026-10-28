package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"time"
	"trackfund/internal/infrastructure/telemetry"
)

func (s *Server) operationalMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.app.Config.OperationsToken == "" {
		http.NotFound(w, r)
		return
	}
	a := sha256.Sum256([]byte(s.app.Config.OperationsToken))
	b := sha256.Sum256([]byte(r.Header.Get("X-Operations-Token")))
	if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		writeAPIError(w, 401, "unauthorized", "Operations authentication required", "")
		return
	}
	if s.app.MonoRepo == nil {
		writeAPIError(w, 503, "metrics_unavailable", "Database metrics unavailable", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	c, err := s.app.MonoRepo.OperationalCounts(ctx)
	if err != nil {
		writeAPIError(w, 503, "metrics_unavailable", "Database metrics unavailable", "")
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	telemetry.Write(w)
	for _, item := range []struct {
		Name  string
		Value int64
	}{{"failed_webhooks", c.FailedWebhooks}, {"stale_pending_sessions", c.PendingSessions}, {"failed_sessions", c.FailedSessions}, {"failed_unlinks", c.FailedUnlinks}, {"avatar_cleanup_failures", c.AvatarCleanupFailures}} {
		fmt.Fprintf(w, "# TYPE trackiq_%s gauge\ntrackiq_%s %d\n", item.Name, item.Name, item.Value)
	}
}
