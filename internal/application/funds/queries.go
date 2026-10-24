package fundsapp

import "trackfund/internal/application/ports"

type FundSummary struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	TargetAmount       int64  `json:"targetAmount"`
	Currency           string `json:"currency"`
	TotalContributions int64  `json:"totalContributions"`
	RemainingAmount    int64  `json:"remainingAmount"`
	ContributionCount  int    `json:"contributionCount"`
}

type FundSummaryReadModel interface {
	GetByID(id string) (FundSummary, bool, error)
	Save(summary FundSummary) error
}

type GetFundSummaryQuery struct {
	UserID string
	FundID string
}

type GetFundSummaryHandler struct {
	readModel  FundSummaryReadModel
	eventStore ports.EventStore
}

func NewGetFundSummaryHandler(readModel FundSummaryReadModel, eventStore ports.EventStore) GetFundSummaryHandler {
	return GetFundSummaryHandler{readModel: readModel, eventStore: eventStore}
}

func (h GetFundSummaryHandler) Handle(query GetFundSummaryQuery) (FundSummary, bool, error) {
	if _, _, err := loadOwnedFund(h.eventStore, query.FundID, query.UserID); err != nil {
		return FundSummary{}, false, err
	}
	return h.readModel.GetByID(query.FundID)
}
