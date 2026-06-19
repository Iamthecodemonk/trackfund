package ports

import (
	"time"

	"trackfund/internal/domain/funds"
)

type EventEnvelope struct {
	EventID    string
	StreamID   string
	Version    int
	OccurredAt time.Time
	Event      funds.Event
}

type EventStore interface {
	Load(streamID string) ([]EventEnvelope, error)
	Append(streamID string, expectedVersion int, events []funds.Event) ([]EventEnvelope, error)
	All() ([]EventEnvelope, error)
	Subscribe(listener func(EventEnvelope) error)
}
