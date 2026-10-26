package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"github.com/go-chi/chi/v5"
	"io"
	"net/http"
	"strings"
	"time"
	monoapp "trackfund/internal/application/mono"
)

func (s *Server) monoWebhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.app == nil || strings.TrimSpace(s.app.Config.MonoWebhookSecret) == "" || s.app.MonoWebhooks == nil {
		writeAPIError(w, 503, "mono_webhook_unavailable", "Webhook storage or verification is not configured", "")
		return
	}
	expected := sha256.Sum256([]byte(s.app.Config.MonoWebhookSecret))
	provided := sha256.Sum256([]byte(r.Header.Get("mono-webhook-secret")))
	if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
		writeAPIError(w, 401, "unauthorized", "Invalid webhook secret", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAPIError(w, 413, "webhook_too_large", "Webhook exceeds 1 MiB", "")
		} else {
			writeAPIError(w, 400, "invalid_webhook", "Could not read webhook", "")
		}
		return
	}
	event, err := monoapp.Decode(body)
	if err != nil {
		writeAPIError(w, 400, "invalid_webhook", "A valid event_id, event, timestamp and data object are required", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err = s.app.MonoWebhooks.EnqueueWebhook(ctx, event, body); err != nil {
		if errors.Is(err, monoapp.ErrEventConflict) {
			writeAPIError(w, 409, "event_id_conflict", "Event ID was already used for a different payload", "")
		} else {
			writeAPIError(w, 503, "webhook_storage_unavailable", "Could not persist webhook; retry delivery", "")
		}
		return
	}
	// No business processing or upstream calls occur before acknowledgement.
	writeAPISuccess(w, http.StatusOK, map[string]bool{"received": true})
}
func (s *Server) getMonoLinkStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.app.MonoWebhooks == nil {
		writeAPIError(w, 503, "mono_sessions_unavailable", "Session status is unavailable", "")
		return
	}
	reference := chi.URLParam(r, "reference")
	if strings.TrimSpace(reference) == "" || len(reference) > 200 {
		writeAPIError(w, 400, "invalid_reference", "Invalid session reference", "")
		return
	}
	result, found, err := s.app.MonoWebhooks.GetLinkSession(r.Context(), reference, currentUser(r).ID)
	if err != nil {
		writeAPIError(w, 503, "mono_status_unavailable", "Could not read session status", "")
		return
	}
	if !found {
		writeAPIError(w, 404, "mono_session_not_found", "Session was not found", "")
		return
	}
	writeAPISuccess(w, http.StatusOK, result)
}
