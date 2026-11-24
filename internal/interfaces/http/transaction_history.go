package httpapi

import (
	"net/http"
	"strings"
	"time"
)

func (s *Server) listTransactions(w http.ResponseWriter, r *http.Request) {
	if s.app == nil || s.app.MonoRepo == nil {
		writeAPIError(w, 503, "transactions_unavailable", "Transaction storage is unavailable", "")
		return
	}
	page, limit, ok := paginationInput(w, r, 50, 100)
	if !ok {
		return
	}
	from, ok := transactionTimeFilter(w, r, "from")
	if !ok {
		return
	}
	to, ok := transactionTimeFilter(w, r, "to")
	if !ok {
		return
	}
	if from != nil && to != nil && !from.Before(*to) {
		writeAPIError(w, 400, "invalid_date_range", "from must be before to", "")
		return
	}
	result, err := s.app.MonoRepo.ListTransactions(r.Context(), currentUser(r).ID, from, to, limit, (page-1)*limit, page)
	if err != nil {
		writeAPIError(w, 500, "transactions_list_failed", "Could not list transactions", "")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeAPISuccess(w, 200, result)
}
func transactionTimeFilter(w http.ResponseWriter, r *http.Request, name string) (*time.Time, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, true
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeAPIError(w, 400, "invalid_date_range", name+" must be an RFC3339 timestamp", "")
		return nil, false
	}
	value = value.UTC()
	return &value, true
}
