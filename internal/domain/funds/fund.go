package funds

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"

	"trackfund/internal/domain/shared"
)

var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

type Fund struct {
	id                 string
	name               string
	targetAmount       int64
	currency           string
	totalContributions int64
	version            int
	changes            []Event
}

func CreateFund(name string, targetAmount int64, currency string) (*Fund, error) {
	name = strings.TrimSpace(name)

	if name == "" {
		return nil, shared.ErrNameRequired
	}
	if targetAmount <= 0 {
		return nil, shared.ErrTargetAmountInvalid
	}
	if !currencyPattern.MatchString(currency) {
		return nil, shared.ErrCurrencyInvalid
	}

	fund := &Fund{}
	fund.record(FundCreated{
		FundID:       newID(),
		Name:         name,
		TargetAmount: targetAmount,
		Currency:     currency,
	})

	return fund, nil
}

func Rehydrate(history []Event) *Fund {
	fund := &Fund{}
	for _, event := range history {
		fund.apply(event)
		fund.version++
	}
	return fund
}

func (f *Fund) ID() string {
	return f.id
}

func (f *Fund) Version() int {
	return f.version
}

func (f *Fund) Changes() []Event {
	return append([]Event(nil), f.changes...)
}

func (f *Fund) MarkChangesCommitted() {
	f.changes = nil
}

func (f *Fund) RecordContribution(amount int64, contributor string) error {
	contributor = strings.TrimSpace(contributor)

	if amount <= 0 {
		return shared.ErrContributionInvalid
	}
	if contributor == "" {
		return shared.ErrContributorRequired
	}

	f.record(ContributionRecorded{
		FundID:      f.id,
		Amount:      amount,
		Contributor: contributor,
	})

	return nil
}

func (f *Fund) record(event Event) {
	f.apply(event)
	f.version++
	f.changes = append(f.changes, event)
}

func (f *Fund) apply(event Event) {
	switch e := event.(type) {
	case FundCreated:
		f.id = e.FundID
		f.name = e.Name
		f.targetAmount = e.TargetAmount
		f.currency = e.Currency
	case ContributionRecorded:
		f.totalContributions += e.Amount
	}
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes[:])
}
