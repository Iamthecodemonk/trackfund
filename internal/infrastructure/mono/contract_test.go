package mono

import (
	"encoding/json"
	"testing"
)

func TestProviderBankDetailsAndSignedTransactions(t *testing.T) {
	var a MonoAccount
	if err := json.Unmarshal([]byte(`{"_id":"bank","name":"Everyday","accountNumber":"0123456789","currency":"NGN","balance":"9007199254740993","institution":{"name":"Example Bank","bankCode":"058"}}`), &a); err != nil || a.ID != "bank" || a.BankName != "Example Bank" || a.BankCode != "058" || a.AccountNumber != "0123456789" || a.Balance == nil || *a.Balance != 9007199254740993 {
		t.Fatal(a, err)
	}
	var tx MonoTransaction
	if err := json.Unmarshal([]byte(`{"id":"tx","type":"debit","amount":500000,"date":"2026-10-28T00:00:00Z"}`), &tx); err != nil || tx.Amount != -500000 {
		t.Fatal(tx, err)
	}
}
