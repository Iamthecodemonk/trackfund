package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	authapp "trackfund/internal/application/auth"
)

type transferInput struct {
	FromTransactionID string `json:"fromTransactionId"`
	ToTransactionID   string `json:"toTransactionId"`
}

func (s *Server) markTransfer(w http.ResponseWriter, r *http.Request) {
	if s.app == nil || s.app.MonoRepo == nil {
		writeAPIError(w, 503, "transactions_unavailable", "Transaction storage is unavailable", "")
		return
	}
	var in transferInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in); err != nil {
		writeAPIError(w, 400, "invalid_json", "Request body must be valid transfer JSON", "")
		return
	}
	in.FromTransactionID = strings.TrimSpace(in.FromTransactionID)
	in.ToTransactionID = strings.TrimSpace(in.ToTransactionID)
	if in.FromTransactionID == "" || in.ToTransactionID == "" || in.FromTransactionID == in.ToTransactionID {
		writeAPIError(w, 400, "invalid_transfer", "Two different transaction IDs are required", "")
		return
	}
	groupID, err := authapp.RandomID()
	if err != nil {
		writeAPIError(w, 500, "transfer_failed", "Could not create transfer relationship", "")
		return
	}
	transactions, err := s.app.MonoRepo.MarkTransfer(r.Context(), currentUser(r).ID, in.FromTransactionID, in.ToTransactionID, groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeAPIError(w, 404, "transaction_not_found", "One or both transactions were not found", "")
		return
	}
	if err != nil {
		writeAPIError(w, 500, "transfer_failed", "Could not mark transfer", "")
		return
	}
	writeAPISuccess(w, 200, map[string]any{"transferGroupId": groupID, "transactions": transactions})
}
