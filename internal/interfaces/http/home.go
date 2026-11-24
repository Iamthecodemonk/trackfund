package httpapi

import (
	"net/http"
	"strings"
	"time"
)

// homeSummary exposes inputs rather than inventing a safe-to-spend value while
// imported history and cash reconciliation are incomplete.
func (s *Server) homeSummary(w http.ResponseWriter, r *http.Request) {
	if s.app == nil || s.app.MonoRepo == nil || s.app.Budgets == nil {
		writeAPIError(w, 503, "home_summary_unavailable", "Home summary storage is unavailable", "")
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("currency")))
	if len(currency) != 3 {
		writeAPIError(w, 400, "invalid_currency", "currency must be a three-letter code", "")
		return
	}
	user := currentUser(r)
	accounts, err := s.app.MonoRepo.ListAccounts(r.Context(), user.ID)
	if err != nil {
		writeAPIError(w, 500, "home_summary_unavailable", "Could not load accounts", "")
		return
	}
	budgets, err := s.app.Budgets.List(r.Context(), user.ID)
	if err != nil {
		writeAPIError(w, 500, "home_summary_unavailable", "Could not load budgets", "")
		return
	}
	now := time.Now().UTC()
	var balance, allocated int64
	accountsUsed := 0
	for _, account := range accounts {
		if account.Currency == currency && account.Balance != nil && account.ConnectionStatus == "connected" {
			balance += *account.Balance
			accountsUsed++
		}
	}
	for _, budget := range budgets {
		if budget.Currency != currency || now.Before(budget.PeriodStart) || !now.Before(budget.PeriodEnd) {
			continue
		}
		for _, category := range budget.Categories {
			allocated += category.Limit
		}
	}
	writeAPISuccess(w, 200, map[string]any{"asOf": now, "currency": currency, "availableBankBalance": balance, "accountsIncluded": accountsUsed, "activeBudgetAllocated": allocated, "safeToSpend": nil, "availability": "incomplete", "reason": "Imported history coverage and cash reconciliation are not complete."})
}
