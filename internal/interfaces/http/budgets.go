package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
	"time"
	authapp "trackfund/internal/application/auth"
	"trackfund/internal/infrastructure/postgres"
)

type budgetInput struct {
	Name        string                    `json:"name"`
	Currency    string                    `json:"currency"`
	Timezone    string                    `json:"timezone"`
	PeriodStart time.Time                 `json:"periodStart"`
	PeriodEnd   time.Time                 `json:"periodEnd"`
	Categories  []postgres.BudgetCategory `json:"categories"`
}

func (s *Server) createBudget(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeBudget(w, r)
	if !ok {
		return
	}
	id, err := authapp.RandomID()
	if err != nil {
		writeBudgetError(w, 500, "budget_create_failed", "Could not create budget")
		return
	}
	b, err := s.app.Budgets.Create(r.Context(), id, currentUser(r).ID, budgetInputValue(in))
	if err != nil {
		writeBudgetError(w, 500, "budget_create_failed", "Could not save budget")
		return
	}
	writeAPISuccess(w, 201, b)
}
func (s *Server) listBudgets(w http.ResponseWriter, r *http.Request) {
	items, err := s.app.Budgets.List(r.Context(), currentUser(r).ID)
	if err != nil {
		writeBudgetError(w, 500, "budgets_list_failed", "Could not list budgets")
		return
	}
	writeAPISuccess(w, 200, items)
}
func (s *Server) getBudget(w http.ResponseWriter, r *http.Request) {
	b, err := s.app.Budgets.Get(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"))
	if errors.Is(err, postgres.ErrBudgetNotFound) {
		writeBudgetError(w, 404, "budget_not_found", "Budget was not found")
		return
	}
	if err != nil {
		writeBudgetError(w, 500, "budget_get_failed", "Could not retrieve budget")
		return
	}
	writeAPISuccess(w, 200, b)
}
func (s *Server) updateBudget(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeBudget(w, r)
	if !ok {
		return
	}
	b, err := s.app.Budgets.Update(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"), budgetInputValue(in))
	if errors.Is(err, postgres.ErrBudgetNotFound) {
		writeBudgetError(w, 404, "budget_not_found", "Budget was not found")
		return
	}
	if err != nil {
		writeBudgetError(w, 500, "budget_update_failed", "Could not update budget")
		return
	}
	writeAPISuccess(w, 200, b)
}
func (s *Server) deleteBudget(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.app.Budgets.Delete(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		writeBudgetError(w, 500, "budget_delete_failed", "Could not delete budget")
		return
	}
	if !deleted {
		writeBudgetError(w, 404, "budget_not_found", "Budget was not found")
		return
	}
	w.WriteHeader(204)
}
func decodeBudget(w http.ResponseWriter, r *http.Request) (budgetInput, bool) {
	var in budgetInput
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		writeBudgetError(w, 400, "invalid_json", "Request body must be valid budget JSON")
		return in, false
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Name == "" || len([]rune(in.Name)) > 160 || len(in.Currency) != 3 || in.Timezone == "" || in.PeriodStart.IsZero() || in.PeriodEnd.IsZero() || !in.PeriodEnd.After(in.PeriodStart) {
		writeBudgetError(w, 400, "invalid_budget", "name, currency, timezone and valid period boundaries are required")
		return in, false
	}
	seen := map[string]bool{}
	for _, c := range in.Categories {
		c.CategoryID = strings.TrimSpace(c.CategoryID)
		if c.CategoryID == "" || c.Limit < 0 || seen[c.CategoryID] {
			writeBudgetError(w, 400, "invalid_budget", "categories require unique IDs and non-negative limits")
			return in, false
		}
		seen[c.CategoryID] = true
	}
	return in, true
}
func budgetInputValue(in budgetInput) postgres.BudgetInput {
	return postgres.BudgetInput{Name: in.Name, Currency: in.Currency, Timezone: in.Timezone, PeriodStart: in.PeriodStart.UTC(), PeriodEnd: in.PeriodEnd.UTC(), Categories: in.Categories}
}
func writeBudgetError(w http.ResponseWriter, status int, code, message string) {
	writeAPIError(w, status, code, message, "")
}
