package fundsapp

import (
	"time"
	"trackfund/internal/application/ports"
)

type FundSummary struct {
	ID                 string    `json:"id"`
	UserID             string    `json:"-"`
	Name               string    `json:"name"`
	TargetAmount       int64     `json:"targetAmount"`
	Currency           string    `json:"currency"`
	TargetIcon         string    `json:"targetIcon,omitempty"`
	TotalContributions int64     `json:"totalContributions"`
	RemainingAmount    int64     `json:"remainingAmount"`
	ContributionCount  int       `json:"contributionCount"`
	CreatedAt          time.Time `json:"createdAt"`
}

type FundSummaryReadModel interface {
	GetByID(id string) (FundSummary, bool, error)
	Save(summary FundSummary) error
	ListByOwner(userID string, limit, offset int) ([]FundSummary, int, error)
}

type ListFundsQuery struct {
	UserID      string
	Page, Limit int
}

type ListFundsResult struct {
	Funds      []FundSummary `json:"funds"`
	Pagination Pagination    `json:"pagination"`
}

type Pagination struct {
	Page    int  `json:"page"`
	Limit   int  `json:"limit"`
	Total   int  `json:"total"`
	HasMore bool `json:"hasMore"`
}

type ListFundsHandler struct{ readModel FundSummaryReadModel }

func NewListFundsHandler(readModel FundSummaryReadModel) ListFundsHandler {
	return ListFundsHandler{readModel: readModel}
}

func (h ListFundsHandler) Handle(query ListFundsQuery) (ListFundsResult, error) {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.Limit < 1 {
		query.Limit = 20
	}
	if query.Limit > 100 {
		query.Limit = 100
	}
	items, total, err := h.readModel.ListByOwner(query.UserID, query.Limit, (query.Page-1)*query.Limit)
	if err != nil {
		return ListFundsResult{}, err
	}
	return ListFundsResult{Funds: items, Pagination: Pagination{Page: query.Page, Limit: query.Limit, Total: total, HasMore: query.Page*query.Limit < total}}, nil
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
