package funds

import "testing"

func TestCreateFundRecordsFundCreatedEvent(t *testing.T) {
	fund, err := CreateFund("owner-a", "School fees", 250000, "NGN")
	if err != nil {
		t.Fatalf("CreateFund returned error: %v", err)
	}

	changes := fund.Changes()
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	event, ok := changes[0].(FundCreated)
	if !ok {
		t.Fatalf("expected FundCreated, got %T", changes[0])
	}
	if event.Name != "School fees" {
		t.Fatalf("expected event name to be trimmed and stored")
	}
}

func TestRecordContributionRecordsEvent(t *testing.T) {
	fund, err := CreateFund("owner-a", "Rent", 400000, "NGN")
	if err != nil {
		t.Fatalf("CreateFund returned error: %v", err)
	}
	fund.MarkChangesCommitted()

	if err := fund.RecordContribution(125000, "Ada"); err != nil {
		t.Fatalf("RecordContribution returned error: %v", err)
	}

	changes := fund.Changes()
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if _, ok := changes[0].(ContributionRecorded); !ok {
		t.Fatalf("expected ContributionRecorded, got %T", changes[0])
	}
}

func TestOwnerSurvivesRehydration(t *testing.T) {
	f, err := CreateFund("owner-a", "Rent", 100, "NGN")
	if err != nil {
		t.Fatal(err)
	}
	if Rehydrate(f.Changes()).OwnerID() != "owner-a" {
		t.Fatal("owner was not persisted in creation event")
	}
	if _, err := CreateFund(" ", "Rent", 100, "NGN"); err == nil {
		t.Fatal("ownerless creation accepted")
	}
}
