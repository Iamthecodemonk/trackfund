package projections

import (
	"sync"

	fundsapp "trackfund/internal/application/funds"
	"trackfund/internal/application/ports"
	"trackfund/internal/domain/funds"
)

type InMemoryFundSummaryReadModel struct {
	mu        sync.RWMutex
	summaries map[string]fundsapp.FundSummary
}

func NewInMemoryFundSummaryReadModel() *InMemoryFundSummaryReadModel {
	return &InMemoryFundSummaryReadModel{
		summaries: make(map[string]fundsapp.FundSummary),
	}
}

func (m *InMemoryFundSummaryReadModel) GetByID(id string) (fundsapp.FundSummary, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	summary, ok := m.summaries[id]
	return summary, ok, nil
}

func (m *InMemoryFundSummaryReadModel) Save(summary fundsapp.FundSummary) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.summaries[summary.ID] = summary
	return nil
}

type FundSummaryProjector struct {
	readModel fundsapp.FundSummaryReadModel
}

func NewFundSummaryProjector(readModel fundsapp.FundSummaryReadModel) FundSummaryProjector {
	return FundSummaryProjector{readModel: readModel}
}

func (p FundSummaryProjector) Attach(eventStore ports.EventStore) {
	eventStore.Subscribe(p.Project)
}

func (p FundSummaryProjector) Rebuild(eventStore ports.EventStore) error {
	events, err := eventStore.All()
	if err != nil {
		return err
	}

	for _, event := range events {
		if err := p.Project(event); err != nil {
			return err
		}
	}

	return nil
}

func (p FundSummaryProjector) Project(envelope ports.EventEnvelope) error {
	switch event := envelope.Event.(type) {
	case funds.FundCreated:
		return p.readModel.Save(fundsapp.FundSummary{
			ID:                 event.FundID,
			Name:               event.Name,
			TargetAmount:       event.TargetAmount,
			Currency:           event.Currency,
			TotalContributions: 0,
			RemainingAmount:    event.TargetAmount,
			ContributionCount:  0,
		})
	case funds.ContributionRecorded:
		current, ok, err := p.readModel.GetByID(event.FundID)
		if err != nil || !ok {
			return err
		}

		total := current.TotalContributions + event.Amount
		remaining := current.TargetAmount - total
		if remaining < 0 {
			remaining = 0
		}

		current.TotalContributions = total
		current.RemainingAmount = remaining
		current.ContributionCount++

		return p.readModel.Save(current)
	default:
		return nil
	}
}
