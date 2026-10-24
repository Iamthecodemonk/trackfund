package fundsapp_test

import (
	"testing"

	fundsapp "trackfund/internal/application/funds"
	"trackfund/internal/infrastructure/eventstore"
	"trackfund/internal/infrastructure/projections"
)

func TestCreateFundProjectsSummary(t *testing.T) {
	store := eventstore.NewInMemoryEventStore()
	readModel := projections.NewInMemoryFundSummaryReadModel()
	projector := projections.NewFundSummaryProjector(readModel)
	projector.Attach(store)

	createFund := fundsapp.NewCreateFundHandler(store)
	getSummary := fundsapp.NewGetFundSummaryHandler(readModel, store)

	result, err := createFund.Handle(fundsapp.CreateFundCommand{
		UserID:       "owner-a",
		Name:         "School fees",
		TargetAmount: 250000,
		Currency:     "NGN",
	})
	if err != nil {
		t.Fatalf("CreateFund returned error: %v", err)
	}

	summary, ok, err := getSummary.Handle(fundsapp.GetFundSummaryQuery{UserID: "owner-a", FundID: result.FundID})
	if err != nil {
		t.Fatalf("GetFundSummary returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected summary to exist")
	}
	if summary.RemainingAmount != 250000 {
		t.Fatalf("expected remaining amount 250000, got %d", summary.RemainingAmount)
	}
}

func TestRecordContributionAppendsEventAndUpdatesSummary(t *testing.T) {
	store := eventstore.NewInMemoryEventStore()
	readModel := projections.NewInMemoryFundSummaryReadModel()
	projector := projections.NewFundSummaryProjector(readModel)
	projector.Attach(store)

	createFund := fundsapp.NewCreateFundHandler(store)
	recordContribution := fundsapp.NewRecordContributionHandler(store)
	getSummary := fundsapp.NewGetFundSummaryHandler(readModel, store)

	result, err := createFund.Handle(fundsapp.CreateFundCommand{
		UserID:       "owner-a",
		Name:         "Rent",
		TargetAmount: 400000,
		Currency:     "NGN",
	})
	if err != nil {
		t.Fatalf("CreateFund returned error: %v", err)
	}

	if err := recordContribution.Handle(fundsapp.RecordContributionCommand{
		UserID:      "owner-a",
		FundID:      result.FundID,
		Amount:      125000,
		Contributor: "Ada",
	}); err != nil {
		t.Fatalf("RecordContribution returned error: %v", err)
	}

	events, err := store.Load(result.FundID)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	summary, ok, err := getSummary.Handle(fundsapp.GetFundSummaryQuery{UserID: "owner-a", FundID: result.FundID})
	if err != nil {
		t.Fatalf("GetFundSummary returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected summary to exist")
	}
	if summary.TotalContributions != 125000 {
		t.Fatalf("expected total contributions 125000, got %d", summary.TotalContributions)
	}
	if summary.RemainingAmount != 275000 {
		t.Fatalf("expected remaining amount 275000, got %d", summary.RemainingAmount)
	}
	if summary.ContributionCount != 1 {
		t.Fatalf("expected contribution count 1, got %d", summary.ContributionCount)
	}
}
