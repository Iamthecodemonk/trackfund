package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) monthlyWrap(w http.ResponseWriter, r *http.Request) {
	if s.app == nil || s.app.MonoRepo == nil {
		writeAPIError(w, 503, "monthly_wrap_unavailable", "Monthly Wrap storage is unavailable", "")
		return
	}
	q := r.URL.Query()
	year, err := strconv.Atoi(q.Get("year"))
	if err != nil || year < 2000 || year > 2100 {
		writeAPIError(w, 400, "invalid_period", "year must be between 2000 and 2100", "")
		return
	}
	month, err := strconv.Atoi(q.Get("month"))
	if err != nil || month < 1 || month > 12 {
		writeAPIError(w, 400, "invalid_period", "month must be between 1 and 12", "")
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(q.Get("currency")))
	if len(currency) != 3 {
		writeAPIError(w, 400, "invalid_currency", "currency must be a three-letter code", "")
		return
	}
	if time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC).After(time.Now().UTC().AddDate(0, 1, 0)) {
		writeAPIError(w, 400, "invalid_period", "period cannot be in the future", "")
		return
	}
	wrap, err := s.app.MonoRepo.MonthlyWrap(r.Context(), currentUser(r).ID, currency, year, month)
	if err != nil {
		writeAPIError(w, 500, "monthly_wrap_failed", "Could not generate Monthly Wrap", "")
		return
	}
	writeAPISuccess(w, 200, wrap)
}
