package eventstore

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"trackfund/internal/application/ports"
	"trackfund/internal/domain/funds"
)

type ConcurrencyError struct {
	StreamID        string
	ExpectedVersion int
	ActualVersion   int
}

func (e ConcurrencyError) Error() string {
	return fmt.Sprintf("expected stream %s to be at version %d, got %d", e.StreamID, e.ExpectedVersion, e.ActualVersion)
}

type InMemoryEventStore struct {
	mu        sync.RWMutex
	streams   map[string][]ports.EventEnvelope
	listeners []func(ports.EventEnvelope) error
}

func NewInMemoryEventStore() *InMemoryEventStore {
	return &InMemoryEventStore{
		streams: make(map[string][]ports.EventEnvelope),
	}
}

func (s *InMemoryEventStore) Load(streamID string) ([]ports.EventEnvelope, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stream := s.streams[streamID]
	return append([]ports.EventEnvelope(nil), stream...), nil
}

func (s *InMemoryEventStore) Append(streamID string, expectedVersion int, events []funds.Event) ([]ports.EventEnvelope, error) {
	s.mu.Lock()

	stream := s.streams[streamID]
	if len(stream) != expectedVersion {
		s.mu.Unlock()
		return nil, ConcurrencyError{StreamID: streamID, ExpectedVersion: expectedVersion, ActualVersion: len(stream)}
	}

	envelopes := make([]ports.EventEnvelope, 0, len(events))
	for index, event := range events {
		envelopes = append(envelopes, ports.EventEnvelope{
			EventID:    newID(),
			StreamID:   streamID,
			Version:    expectedVersion + index + 1,
			OccurredAt: time.Now().UTC(),
			Event:      event,
		})
	}

	s.streams[streamID] = append(stream, envelopes...)
	listeners := append([]func(ports.EventEnvelope) error(nil), s.listeners...)
	s.mu.Unlock()

	for _, envelope := range envelopes {
		for _, listener := range listeners {
			if err := listener(envelope); err != nil {
				return envelopes, err
			}
		}
	}

	return envelopes, nil
}

func (s *InMemoryEventStore) All() ([]ports.EventEnvelope, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var events []ports.EventEnvelope
	for _, stream := range s.streams {
		events = append(events, stream...)
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].OccurredAt.Before(events[j].OccurredAt)
	})

	return events, nil
}

func (s *InMemoryEventStore) Subscribe(listener func(ports.EventEnvelope) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, listener)
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes[:])
}
