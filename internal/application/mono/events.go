package mono

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrInvalidEvent = errors.New("invalid_webhook")
var ErrOwnershipConflict = errors.New("ownership_conflict")
var ErrMappingPending = errors.New("mapping_pending")
var ErrEventConflict = errors.New("event_id_conflict")

type Event struct {
	ID        string          `json:"event_id"`
	Type      string          `json:"event"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

func Decode(body []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(body, &e); err != nil || strings.TrimSpace(e.ID) == "" || len(e.ID) > 200 || strings.TrimSpace(e.Type) == "" || len(e.Type) > 120 || len(e.Data) == 0 || bytes.TrimSpace(e.Data)[0] != '{' {
		return e, ErrInvalidEvent
	}
	if e.Known() && e.Timestamp.IsZero() {
		return e, ErrInvalidEvent
	}
	return e, nil
}
func (e Event) Known() bool {
	switch e.Type {
	case "mono.events.account_connected", "mono.events.account_updated", "mono.events.account_created", "mono.events.account_unlinked", "mono.events.transactions_created", "mono.events.transaction_created", "mono.events.transactions.created":
		return true
	}
	return false
}
func (e Event) IsTransactions() bool { return strings.Contains(e.Type, "transaction") }

type Meta struct {
	Ref           string   `json:"ref"`
	DataStatus    string   `json:"data_status"`
	RetrievedData []string `json:"retrieved_data"`
}
type Account struct {
	ID            string `json:"id"`
	MongoID       string `json:"_id"`
	Name          string `json:"name"`
	AccountNumber string `json:"accountNumber"`
	LegacyNumber  string `json:"account_number"`
	BankCode      string `json:"bank_code"`
	Currency      string `json:"currency"`
	Institution   struct {
		BankCode string `json:"bankCode"`
		Name     string `json:"name"`
	} `json:"institution"`
}
type AccountEvent struct {
	Account  Account
	Meta     Meta
	Snapshot json.RawMessage
}

func (e Event) AccountUpdate() (AccountEvent, error) {
	var d struct {
		Account Account `json:"account"`
		ID      string  `json:"id"`
		Meta    Meta    `json:"meta"`
	}
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return AccountEvent{}, ErrInvalidEvent
	}
	if d.Account.ID == "" {
		d.Account.ID = d.Account.MongoID
	}
	if d.Account.ID == "" {
		d.Account.ID = d.ID
	}
	if strings.TrimSpace(d.Account.ID) == "" || len(d.Account.ID) > 200 || len(d.Meta.Ref) > 200 {
		return AccountEvent{}, ErrInvalidEvent
	}
	if d.Account.AccountNumber == "" {
		d.Account.AccountNumber = d.Account.LegacyNumber
	}
	if d.Account.BankCode == "" {
		d.Account.BankCode = d.Account.Institution.BankCode
	}
	d.Meta.DataStatus = strings.ToUpper(d.Meta.DataStatus)
	if e.Type == "mono.events.account_unlinked" {
		d.Meta.DataStatus = "DISCONNECTED"
	}
	switch d.Meta.DataStatus {
	case "", "PROCESSING", "AVAILABLE", "PARTIAL", "UNAVAILABLE", "FAILED", "DISCONNECTED":
	default:
		return AccountEvent{}, ErrInvalidEvent
	}
	// Normalize readiness separately from bank details and retain integer balances as raw JSON.
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(e.Data, &snapshot); err != nil {
		return AccountEvent{}, ErrInvalidEvent
	}
	meta, _ := json.Marshal(d.Meta)
	snapshot["meta"] = meta
	if d.Account.Institution.Name != "" {
		name, _ := json.Marshal(d.Account.Institution.Name)
		snapshot["bank_name"] = name
	}
	normalized, _ := json.Marshal(snapshot)
	return AccountEvent{Account: d.Account, Meta: d.Meta, Snapshot: normalized}, nil
}

type Transaction struct {
	Type        string    `json:"type,omitempty"`
	ID          string    `json:"id"`
	AccountID   string    `json:"account_id"`
	Amount      int64     `json:"amount"`
	Currency    string    `json:"currency"`
	PaidAt      time.Time `json:"paid_at"`
	Description string    `json:"description"`
}

func (e Event) Transactions() ([]Transaction, error) {
	var d struct {
		Transactions []json.RawMessage `json:"transactions"`
	}
	if err := json.Unmarshal(e.Data, &d); err != nil || d.Transactions == nil {
		return nil, ErrInvalidEvent
	}
	result := make([]Transaction, 0, len(d.Transactions))
	for _, raw := range d.Transactions {
		var tx Transaction
		if err := json.Unmarshal(raw, &tx); err != nil || tx.ID == "" || tx.AccountID == "" || tx.Currency == "" || tx.PaidAt.IsZero() {
			return nil, ErrInvalidEvent
		}
		var signErr error
		tx.Amount, signErr = SignedAmount(tx.Amount, tx.Type)
		if signErr != nil {
			return nil, ErrInvalidEvent
		}
		result = append(result, tx)
	}
	return result, nil
}
func TransactionsAvailable(metadata json.RawMessage) bool {
	var d struct {
		Meta Meta `json:"meta"`
	}
	if json.Unmarshal(metadata, &d) != nil {
		return false
	}
	if strings.EqualFold(d.Meta.DataStatus, "AVAILABLE") {
		return true
	}
	if strings.EqualFold(d.Meta.DataStatus, "PARTIAL") {
		for _, item := range d.Meta.RetrievedData {
			if item == "transactions" {
				return true
			}
		}
	}
	return false
}

type SessionStatus struct {
	Reference         string   `json:"reference"`
	Status            string   `json:"status"`
	AccountID         *string  `json:"accountId"`
	DataStatus        string   `json:"dataStatus"`
	AvailableData     []string `json:"availableData"`
	TransactionsReady bool     `json:"transactionsReady"`
	SyncStatus        string   `json:"syncStatus"`
}
