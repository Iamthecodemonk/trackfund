package fundsapp_test

import (
	"errors"
	"testing"
	app "trackfund/internal/application/funds"
	"trackfund/internal/domain/funds"
	"trackfund/internal/infrastructure/eventstore"
	"trackfund/internal/infrastructure/projections"
)

func TestOwnershipEnforcedWithoutHTTP(t *testing.T) {
	store := eventstore.NewInMemoryEventStore()
	rm := projections.NewInMemoryFundSummaryReadModel()
	projections.NewFundSummaryProjector(rm).Attach(store)
	create := app.NewCreateFundHandler(store)
	get := app.NewGetFundSummaryHandler(rm, store)
	contribute := app.NewRecordContributionHandler(store)
	result, err := create.Handle(app.CreateFundCommand{UserID: "alice", Name: "Rent", TargetAmount: 100, Currency: "NGN"})
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"bob", ""} {
		want := app.ErrFundForbidden
		if user == "" {
			want = app.ErrFundAuthenticationRequired
		}
		if _, _, err := get.Handle(app.GetFundSummaryQuery{UserID: user, FundID: result.FundID}); !errors.Is(err, want) {
			t.Fatalf("read: %v", err)
		}
		if err := contribute.Handle(app.RecordContributionCommand{UserID: user, FundID: result.FundID, Amount: 10, Contributor: "test"}); !errors.Is(err, want) {
			t.Fatalf("write: %v", err)
		}
	}
	history, _ := store.Load(result.FundID)
	if len(history) != 1 {
		t.Fatal("unauthorized contribution changed stream")
	}
	if _, err := create.Handle(app.CreateFundCommand{Name: "Ownerless", TargetAmount: 100, Currency: "NGN"}); !errors.Is(err, app.ErrFundAuthenticationRequired) {
		t.Fatal(err)
	}
	// An old creation event has no OwnerID and must never be claimed by a reader.
	_, err = store.Append("legacy", 0, []funds.Event{funds.FundCreated{FundID: "legacy", Name: "Old", TargetAmount: 100, Currency: "NGN"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"legacy", "missing"} {
		if _, _, err := get.Handle(app.GetFundSummaryQuery{UserID: "alice", FundID: id}); !errors.Is(err, app.ErrFundNotFound) {
			t.Fatalf("%s read: %v", id, err)
		}
		if err := contribute.Handle(app.RecordContributionCommand{UserID: "alice", FundID: id, Amount: 1, Contributor: "test"}); !errors.Is(err, app.ErrFundNotFound) {
			t.Fatalf("%s write: %v", id, err)
		}
	}
}
