package ports

import "context"

// MonoSessionStore persists correlation before the provider can send a webhook.
type MonoSessionStore interface {
	CreateLinkSession(ctx context.Context, reference, userID, redirectURL string) error
}
