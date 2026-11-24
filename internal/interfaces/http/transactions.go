package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"trackfund/internal/infrastructure/postgres"
)

type transactionMetadataInput struct {
	Category        *string `json:"category"`
	CategoryID      *string `json:"categoryId"`
	UserDescription *string `json:"userDescription"`
	PersonalNote    *string `json:"personalNote"`
}

// updateTransactionMetadata changes only user-owned annotations. Provider data,
// money fields, paidAt and both transaction identifiers are never mutable here.
func (s *Server) updateTransactionMetadata(w http.ResponseWriter, r *http.Request) {
	if s.app == nil || s.app.MonoRepo == nil {
		writeAPIError(w, 503, "transactions_unavailable", "Transaction storage is unavailable", "")
		return
	}
	var in transactionMetadataInput
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		writeAPIError(w, 400, "invalid_json", "Request body must be valid transaction metadata JSON", "")
		return
	}
	if in.Category == nil && in.CategoryID == nil && in.UserDescription == nil && in.PersonalNote == nil {
		writeAPIError(w, 400, "missing_fields", "Send category, categoryId, userDescription or personalNote", "")
		return
	}
	for _, field := range []struct {
		value *string
		name  string
		max   int
	}{{in.Category, "category", 80}, {in.CategoryID, "categoryId", 80}, {in.UserDescription, "userDescription", 400}, {in.PersonalNote, "personalNote", 2000}} {
		if field.value != nil && len([]rune(strings.TrimSpace(*field.value))) > field.max {
			writeAPIError(w, 400, "invalid_transaction_metadata", field.name+" is too long", "")
			return
		}
	}
	trim := func(v *string) *string {
		if v == nil {
			return nil
		}
		s := strings.TrimSpace(*v)
		return &s
	}
	transaction, err := s.app.MonoRepo.UpdateTransactionMetadata(r.Context(), currentUser(r).ID, chi.URLParam(r, "id"), postgres.TransactionUserMetadata{Category: trim(in.Category), CategoryID: trim(in.CategoryID), UserDescription: trim(in.UserDescription), PersonalNote: trim(in.PersonalNote)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeAPIError(w, 404, "transaction_not_found", "Transaction was not found", "")
			return
		}
		writeAPIError(w, 500, "transaction_update_failed", "Could not update transaction", "")
		return
	}
	writeAPISuccess(w, 200, transaction)
}
