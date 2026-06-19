package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"trackfund/internal/application/ports"
	"trackfund/internal/domain/funds"
)

type EventStore struct {
	pool      *pgxpool.Pool
	listeners []func(ports.EventEnvelope) error
}

func NewEventStore(pool *pgxpool.Pool) *EventStore {
	return &EventStore{pool: pool}
}

func (s *EventStore) Load(streamID string) ([]ports.EventEnvelope, error) {
	rows, err := s.pool.Query(
		context.Background(),
		`SELECT event_id, stream_id, version, event_type, event_data, occurred_at
		 FROM event_store
		 WHERE stream_id = $1
		 ORDER BY version ASC`,
		streamID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEvents(rows)
}

func (s *EventStore) Append(streamID string, expectedVersion int, events []funds.Event) ([]ports.EventEnvelope, error) {
	ctx := context.Background()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, streamID); err != nil {
		return nil, err
	}

	var actualVersion int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM event_store WHERE stream_id = $1`, streamID).Scan(&actualVersion); err != nil {
		return nil, err
	}
	if actualVersion != expectedVersion {
		return nil, fmt.Errorf("expected stream %s to be at version %d, got %d", streamID, expectedVersion, actualVersion)
	}

	envelopes := make([]ports.EventEnvelope, 0, len(events))
	for index, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}

		envelope := ports.EventEnvelope{
			EventID:    newID(),
			StreamID:   streamID,
			Version:    expectedVersion + index + 1,
			OccurredAt: time.Now().UTC(),
			Event:      event,
		}

		if _, err := tx.Exec(
			ctx,
			`INSERT INTO event_store (event_id, stream_id, version, event_type, event_data, occurred_at)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			envelope.EventID,
			envelope.StreamID,
			envelope.Version,
			string(event.Type()),
			payload,
			envelope.OccurredAt,
		); err != nil {
			return nil, err
		}

		envelopes = append(envelopes, envelope)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	for _, envelope := range envelopes {
		for _, listener := range s.listeners {
			if err := listener(envelope); err != nil {
				return envelopes, err
			}
		}
	}

	return envelopes, nil
}

func (s *EventStore) All() ([]ports.EventEnvelope, error) {
	rows, err := s.pool.Query(
		context.Background(),
		`SELECT event_id, stream_id, version, event_type, event_data, occurred_at
		 FROM event_store
		 ORDER BY id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].OccurredAt.Before(events[j].OccurredAt)
	})

	return events, nil
}

func (s *EventStore) Subscribe(listener func(ports.EventEnvelope) error) {
	s.listeners = append(s.listeners, listener)
}

func scanEvents(rows pgx.Rows) ([]ports.EventEnvelope, error) {
	var events []ports.EventEnvelope

	for rows.Next() {
		var envelope ports.EventEnvelope
		var eventType string
		var data []byte

		if err := rows.Scan(
			&envelope.EventID,
			&envelope.StreamID,
			&envelope.Version,
			&eventType,
			&data,
			&envelope.OccurredAt,
		); err != nil {
			return nil, err
		}

		event, err := decodeEvent(eventType, data)
		if err != nil {
			return nil, err
		}

		envelope.Event = event
		events = append(events, envelope)
	}

	return events, rows.Err()
}

func decodeEvent(eventType string, data []byte) (funds.Event, error) {
	switch funds.EventType(eventType) {
	case funds.EventFundCreated:
		var event funds.FundCreated
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		return event, nil
	case funds.EventContributionRecorded:
		var event funds.ContributionRecorded
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		return event, nil
	default:
		return nil, fmt.Errorf("unknown event type %s", eventType)
	}
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes[:])
}
