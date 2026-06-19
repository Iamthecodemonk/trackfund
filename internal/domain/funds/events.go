package funds

type EventType string

const (
	EventFundCreated          EventType = "fund.created"
	EventContributionRecorded EventType = "fund.contribution_recorded"
)

type Event interface {
	Type() EventType
	AggregateID() string
}

type FundCreated struct {
	FundID       string
	Name         string
	TargetAmount int64
	Currency     string
}

func (e FundCreated) Type() EventType {
	return EventFundCreated
}
func (e FundCreated) AggregateID() string {
	return e.FundID
}

type ContributionRecorded struct {
	FundID      string
	Amount      int64
	Contributor string
}

func (e ContributionRecorded) Type() EventType {
	return EventContributionRecorded
}
func (e ContributionRecorded) AggregateID() string {
	return e.FundID
}
