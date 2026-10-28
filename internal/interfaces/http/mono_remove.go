package httpapi

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) deleteMonoAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.app.MonoRepo == nil {
		writeAPIError(w, 503, "mono_unavailable", "Account storage is unavailable", "")
		return
	}
	found, err := s.app.MonoRepo.DeleteAccount(r.Context(), chi.URLParam(r, "id"), currentUser(r).ID)
	if err != nil {
		writeAPIError(w, 503, "account_removal_failed", "Could not remove account; retry", "")
		return
	}
	if !found {
		writeAPIError(w, 404, "account_not_found", "Account was not found", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
