package fundsapp

import (
	"errors"

	"trackfund/internal/application/ports"
	"trackfund/internal/domain/funds"
)

var ErrFundNotFound = errors.New("fund was not found")

type CreateFundCommand struct {
	Name         string `json:"name"`
	TargetAmount int64  `json:"targetAmount"`
	Currency     string `json:"currency"`
}

type CreateFundResult struct {
	FundID string `json:"fundId"`
}

type CreateFundHandler struct {
	eventStore ports.EventStore
}

func NewCreateFundHandler(eventStore ports.EventStore) CreateFundHandler {
	return CreateFundHandler{eventStore: eventStore}
}

func (h CreateFundHandler) Handle(command CreateFundCommand) (CreateFundResult, error) {
	fund, err := funds.CreateFund(command.Name, command.TargetAmount, command.Currency)
	if err != nil {
		return CreateFundResult{}, err
	}

	if _, err := h.eventStore.Append(fund.ID(), 0, fund.Changes()); err != nil {
		return CreateFundResult{}, err
	}
	fund.MarkChangesCommitted()

	return CreateFundResult{FundID: fund.ID()}, nil
}

type RecordContributionCommand struct {
	FundID      string
	Amount      int64  `json:"amount"`
	Contributor string `json:"contributor"`
}

type RecordContributionHandler struct {
	eventStore ports.EventStore
}

func NewRecordContributionHandler(eventStore ports.EventStore) RecordContributionHandler {
	return RecordContributionHandler{eventStore: eventStore}
}

func (h RecordContributionHandler) Handle(command RecordContributionCommand) error {
	history, err := h.eventStore.Load(command.FundID)
	if err != nil {
		return err
	}
	if len(history) == 0 {
		return ErrFundNotFound
	}

	events := make([]funds.Event, 0, len(history))
	for _, envelope := range history {
		events = append(events, envelope.Event)
	}

	fund := funds.Rehydrate(events)
	if err := fund.RecordContribution(command.Amount, command.Contributor); err != nil {
		return err
	}

	if _, err := h.eventStore.Append(command.FundID, len(history), fund.Changes()); err != nil {
		return err
	}
	fund.MarkChangesCommitted()

	return nil
}
