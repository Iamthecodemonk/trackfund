package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	authapp "trackfund/internal/application/auth"
	"trackfund/internal/infrastructure/postgres"

	"github.com/go-chi/chi/v5"
)

const maxManualExpenseBody = 16 * 1024

type manualExpenseInput struct {
	Amount        *int64     `json:"amount"`
	Currency      *string    `json:"currency"`
	PaymentMethod *string    `json:"paymentMethod"`
	Category      *string    `json:"category"`
	Description   *string    `json:"description"`
	Note          *string    `json:"note"`
	OccurredAt    *time.Time `json:"occurredAt"`
}

func (s *Server) createManualExpense(w http.ResponseWriter, r *http.Request) {
	if s.app == nil || s.app.ManualExpenses == nil {
		writeManualExpenseError(w, http.StatusServiceUnavailable, "expenses_unavailable", "Manual expense storage is unavailable")
		return
	}
	user := currentUser(r)
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) == 0 || len(key) > 200 {
		writeManualExpenseError(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is required and must be at most 200 characters")
		return
	}
	input, ok := decodeManualExpenseInput(w, r)
	if !ok {
		return
	}
	if input.Amount == nil || input.Currency == nil || input.PaymentMethod == nil || input.Description == nil {
		writeManualExpenseError(w, http.StatusBadRequest, "missing_fields", "amount, currency, paymentMethod and description are required")
		return
	}
	if err := validateManualExpense(input, true); err != nil {
		writeManualExpenseError(w, http.StatusBadRequest, "invalid_expense", err.Error())
		return
	}
	id, err := authapp.RandomID()
	if err != nil {
		writeManualExpenseError(w, http.StatusInternalServerError, "expense_id_failed", "Could not create expense")
		return
	}
	occurredAt := time.Now().UTC()
	if input.OccurredAt != nil {
		occurredAt = input.OccurredAt.UTC()
	}
	expense, replayed, err := s.app.ManualExpenses.Create(r.Context(), postgres.CreateManualExpense{
		ID: id, UserID: user.ID, IdempotencyKey: key, Amount: *input.Amount,
		Currency: strings.ToUpper(strings.TrimSpace(*input.Currency)), PaymentMethod: strings.ToLower(strings.TrimSpace(*input.PaymentMethod)),
		Category: optionalString(input.Category), Description: strings.TrimSpace(*input.Description), Note: optionalString(input.Note), OccurredAt: occurredAt,
	})
	if err != nil {
		if errors.Is(err, postgres.ErrManualExpenseIdempotencyConflict) {
			writeManualExpenseError(w, http.StatusConflict, "idempotency_key_conflict", "This Idempotency-Key belongs to a deleted expense; use a new key")
			return
		}
		writeManualExpenseError(w, http.StatusInternalServerError, "expense_create_failed", "Could not save expense")
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeAPISuccess(w, status, map[string]any{"expense": expense, "idempotentReplay": replayed})
}

func (s *Server) listManualExpenses(w http.ResponseWriter, r *http.Request) {
	if s.app == nil || s.app.ManualExpenses == nil {
		writeManualExpenseError(w, http.StatusServiceUnavailable, "expenses_unavailable", "Manual expense storage is unavailable")
		return
	}
	expenses, err := s.app.ManualExpenses.List(r.Context(), currentUser(r).ID)
	if err != nil {
		writeManualExpenseError(w, http.StatusInternalServerError, "expenses_unavailable", "Could not list expenses")
		return
	}
	writeAPISuccess(w, http.StatusOK, expenses)
}

func (s *Server) updateManualExpense(w http.ResponseWriter, r *http.Request) {
	if s.app == nil || s.app.ManualExpenses == nil {
		writeManualExpenseError(w, http.StatusServiceUnavailable, "expenses_unavailable", "Manual expense storage is unavailable")
		return
	}
	input, ok := decodeManualExpenseInput(w, r)
	if !ok {
		return
	}
	if input.Amount == nil && input.Currency == nil && input.PaymentMethod == nil && input.Category == nil && input.Description == nil && input.Note == nil && input.OccurredAt == nil {
		writeManualExpenseError(w, http.StatusBadRequest, "missing_fields", "Send at least one editable expense field")
		return
	}
	if err := validateManualExpense(input, false); err != nil {
		writeManualExpenseError(w, http.StatusBadRequest, "invalid_expense", err.Error())
		return
	}
	expense, err := s.app.ManualExpenses.Update(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"), postgres.UpdateManualExpense{
		Amount: input.Amount, Currency: normalizeCurrency(input.Currency), PaymentMethod: normalizePaymentMethod(input.PaymentMethod),
		Category: normalizeOptional(input.Category), Description: trimOptional(input.Description), Note: normalizeOptional(input.Note), OccurredAt: utcOptional(input.OccurredAt),
	})
	if errors.Is(err, postgres.ErrManualExpenseNotFound) {
		writeManualExpenseError(w, http.StatusNotFound, "expense_not_found", "Expense was not found")
		return
	}
	if err != nil {
		writeManualExpenseError(w, http.StatusInternalServerError, "expense_update_failed", "Could not update expense")
		return
	}
	writeAPISuccess(w, http.StatusOK, expense)
}

func (s *Server) deleteManualExpense(w http.ResponseWriter, r *http.Request) {
	if s.app == nil || s.app.ManualExpenses == nil {
		writeManualExpenseError(w, http.StatusServiceUnavailable, "expenses_unavailable", "Manual expense storage is unavailable")
		return
	}
	deleted, err := s.app.ManualExpenses.Delete(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"))
	if err != nil {
		writeManualExpenseError(w, http.StatusInternalServerError, "expense_delete_failed", "Could not remove expense")
		return
	}
	if !deleted {
		writeManualExpenseError(w, http.StatusNotFound, "expense_not_found", "Expense was not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeManualExpenseInput(w http.ResponseWriter, r *http.Request) (manualExpenseInput, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxManualExpenseBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input manualExpenseInput
	if err := decoder.Decode(&input); err != nil {
		writeManualExpenseError(w, http.StatusBadRequest, "invalid_json", "Request body must be valid expense JSON")
		return input, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeManualExpenseError(w, http.StatusBadRequest, "invalid_json", "Send a single JSON object")
		return input, false
	}
	return input, true
}

func validateManualExpense(in manualExpenseInput, _ bool) error {
	if in.Amount != nil && *in.Amount <= 0 {
		return errors.New("amount must be a positive integer in minor units")
	}
	if in.Currency != nil {
		currency := strings.ToUpper(strings.TrimSpace(*in.Currency))
		if len(currency) != 3 {
			return errors.New("currency must be a three-letter code")
		}
		for _, letter := range currency {
			if letter < 'A' || letter > 'Z' {
				return errors.New("currency must be a three-letter code")
			}
		}
	}
	if in.PaymentMethod != nil {
		switch strings.ToLower(strings.TrimSpace(*in.PaymentMethod)) {
		case "cash", "card", "transfer", "other":
		default:
			return errors.New("paymentMethod must be cash, card, transfer or other")
		}
	}
	if in.Category != nil && len([]rune(strings.TrimSpace(*in.Category))) > 80 {
		return errors.New("category must be at most 80 characters")
	}
	if in.Description != nil && (strings.TrimSpace(*in.Description) == "" || len([]rune(strings.TrimSpace(*in.Description))) > 400) {
		return errors.New("description must contain 1 to 400 characters")
	}
	if in.Note != nil && len([]rune(strings.TrimSpace(*in.Note))) > 2000 {
		return errors.New("note must be at most 2000 characters")
	}
	if in.OccurredAt != nil && in.OccurredAt.IsZero() {
		return errors.New("occurredAt must be an RFC3339 timestamp")
	}
	return nil
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
func normalizeOptional(value *string) *string {
	if value == nil {
		return nil
	}
	result := strings.TrimSpace(*value)
	return &result
}
func trimOptional(value *string) *string { return normalizeOptional(value) }
func normalizeCurrency(value *string) *string {
	if value == nil {
		return nil
	}
	result := strings.ToUpper(strings.TrimSpace(*value))
	return &result
}
func normalizePaymentMethod(value *string) *string {
	if value == nil {
		return nil
	}
	result := strings.ToLower(strings.TrimSpace(*value))
	return &result
}
func utcOptional(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}

func writeManualExpenseError(w http.ResponseWriter, status int, code, message string) {
	writeAPIError(w, status, code, message, "")
}
