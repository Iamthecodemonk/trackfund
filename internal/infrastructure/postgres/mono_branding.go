package postgres

import (
	"encoding/json"
	"strings"
	monoapp "trackfund/internal/application/mono"
)

func (a *MonoAccount) applyBranding() {
	var m struct {
		BankName string `json:"bank_name"`
		Account  struct {
			Institution struct {
				Name string `json:"name"`
			} `json:"institution"`
		} `json:"account"`
	}
	_ = json.Unmarshal(a.Metadata, &m)
	a.BankName = strings.TrimSpace(m.Account.Institution.Name)
	if a.BankName == "" {
		a.BankName = strings.TrimSpace(m.BankName)
	}
	if a.BankName == "" {
		a.BankName = monoapp.BankName(a.BankCode)
	}
	a.BankLogoURL = nil // No unverified provider URLs are exposed.
}
func transactionDirection(raw json.RawMessage) string {
	var m struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Type
}
