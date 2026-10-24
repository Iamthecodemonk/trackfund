package fundsapp

import (
	"errors"
	"strings"

	"trackfund/internal/application/ports"
	"trackfund/internal/domain/funds"
)

var ErrFundNotFound = errors.New("fund was not found")
var ErrFundForbidden = errors.New("fund belongs to another user")
var ErrFundAuthenticationRequired = errors.New("authentication is required")

type CreateFundCommand struct {
	UserID       string `json:"-"`
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
	if strings.TrimSpace(command.UserID) == "" {
		return CreateFundResult{}, ErrFundAuthenticationRequired
	}
	fund, err := funds.CreateFund(command.UserID, command.Name, command.TargetAmount, command.Currency)
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
	UserID      string `json:"-"`
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
	fund, version, err := loadOwnedFund(h.eventStore, command.FundID, command.UserID)
	if err != nil {
		return err
	}
	if err := fund.RecordContribution(command.Amount, command.Contributor); err != nil {
		return err
	}

	if _, err := h.eventStore.Append(command.FundID, version, fund.Changes()); err != nil {
		return err
	}
	fund.MarkChangesCommitted()

	return nil
}

// Ownership comes from the immutable creation event, never a request body or cache.
// Legacy events without an owner stay inaccessible until ownership is verified.
func loadOwnedFund(store ports.EventStore, fundID, userID string) (*funds.Fund, int, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, 0, ErrFundAuthenticationRequired
	}
	history, err := store.Load(fundID)
	if err != nil {
		return nil, 0, err
	}
	if len(history) == 0 {
		return nil, 0, ErrFundNotFound
	}
	events := make([]funds.Event, 0, len(history))
	for _, envelope := range history {
		events = append(events, envelope.Event)
	}
	fund := funds.Rehydrate(events)
	if fund.OwnerID() == "" {
		return nil, 0, ErrFundNotFound
	}
	if fund.OwnerID() != userID {
		return nil, 0, ErrFundForbidden
	}
	return fund, len(history), nil
}
