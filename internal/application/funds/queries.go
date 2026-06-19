package fundsapp

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
	FundID string
}

type GetFundSummaryHandler struct {
	readModel FundSummaryReadModel
}

func NewGetFundSummaryHandler(readModel FundSummaryReadModel) GetFundSummaryHandler {
	return GetFundSummaryHandler{readModel: readModel}
}

func (h GetFundSummaryHandler) Handle(query GetFundSummaryQuery) (FundSummary, bool, error) {
	return h.readModel.GetByID(query.FundID)
}
