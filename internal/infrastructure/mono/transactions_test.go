package mono

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type pageTransport func(*http.Request) (*http.Response, error)

func (f pageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestV2TransactionsPaginationAndPrecision(t *testing.T) {
	c := NewClient("https://api.withmono.com/v2", "secret", "")
	calls := 0
	c.HTTPClient = &http.Client{Transport: pageTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		page := r.URL.Query().Get("page")
		body := ""
		if r.URL.Host != "api.withmono.com" || !strings.HasPrefix(r.URL.Path, "/v2/accounts/bank/transactions") {
			t.Fatal("followed untrusted next URL")
		}
		if page == "1" {
			body = `{"data":[{"id":"tx1","amount":9007199254740993,"date":"2026-10-26T09:00:00Z","narration":"First"}],"meta":{"next":"https://evil.example/steal-key?page=2"}}`
		} else if page == "2" {
			body = `{"data":{"transactions":[{"_id":"tx2","amount":5,"date":"2026-10-26T09:00:00Z","narration":"Second"}],"paging":{"next":null}}}`
		} else {
			t.Fatal("unexpected page")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	result, err := c.GetTransactions(context.Background(), "bank")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(result.Data.Transactions) != 2 {
		t.Fatal("incomplete pagination")
	}
	if result.Data.Transactions[0].Amount != 9007199254740993 || result.Data.Transactions[1].ID != "tx2" || result.Data.Transactions[1].PaidAt == "" || result.Data.Transactions[1].Description != "Second" {
		t.Fatal("incorrect v2 mapping")
	}
}
func TestMissingTransactionsAreNotEmptySuccess(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":{}}`, `{"data":[{"amount":1.5}]}`} {
		var r TransactionsResponse
		if json.Unmarshal([]byte(body), &r) == nil {
			t.Fatal("invalid transaction data accepted", body)
		}
	}
	for _, body := range []string{`{"data":[]}`, `{"data":{"transactions":[]}}`} {
		var r TransactionsResponse
		if err := json.Unmarshal([]byte(body), &r); err != nil || r.Data.Transactions == nil {
			t.Fatal("valid empty list rejected", err)
		}
	}
}
