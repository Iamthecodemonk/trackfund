package mono

import (
	"encoding/json"
	"testing"
)

func TestReadinessAndIntegerValidation(t *testing.T) {
	for _, tt := range []struct {
		body string
		want bool
	}{
		{`{"meta":{"data_status":"AVAILABLE"}}`, true},
		{`{"meta":{"data_status":"PARTIAL","retrieved_data":["transactions"]}}`, true},
		{`{"meta":{"data_status":"PARTIAL","retrieved_data":["balance"]}}`, false},
		{`{"meta":{"data_status":"PROCESSING"}}`, false},
	} {
		if TransactionsAvailable(json.RawMessage(tt.body)) != tt.want {
			t.Fatal(tt.body)
		}
	}
	for _, body := range []string{`{}`, `{"event_id":"e","event":"mono.events.account_connected","data":{}}`, `{"event_id":"e","event":"other","data":null}`} {
		if _, err := Decode([]byte(body)); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
	e := Event{Data: json.RawMessage(`{"transactions":[{"id":"t","account_id":"a","amount":1.5,"currency":"NGN","paid_at":"2026-10-26T09:00:00Z"}]}`)}
	if _, err := e.Transactions(); err == nil {
		t.Fatal("fractional amount accepted")
	}
}
