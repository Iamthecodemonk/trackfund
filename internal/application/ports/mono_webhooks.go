package ports

import (
	"context"
	monoapp "trackfund/internal/application/mono"
)

type MonoWebhookStore interface {
	EnqueueWebhook(context.Context, monoapp.Event, []byte) error
	GetLinkSession(context.Context, string, string) (monoapp.SessionStatus, bool, error)
}
