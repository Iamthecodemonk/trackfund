package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	fundsapp "trackfund/internal/application/funds"
	"trackfund/internal/composition"
)

type Server struct {
	app    *composition.App
	router chi.Router
}

func NewServer(app *composition.App) *Server {
	server := &Server{
		app:    app,
		router: chi.NewRouter(),
	}
	// attach request logging middleware when available
	if app != nil && app.Logger != nil {
		server.router.Use(app.Logger.RequestMiddleware)
	}
	server.routes()
	return server
}

func (s *Server) Handler() http.Handler {
	return s.router
}

func (s *Server) routes() {
	s.router.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", s.register)
		r.Post("/verify-otp", s.verifyOTP)
		r.Post("/resend-otp", s.resendOTP)
		r.Post("/login", s.login)
		r.Post("/refresh-token", s.refreshToken)
		r.Post("/logout", s.logout)
		r.Post("/forgot-password", s.forgotPassword)
		r.Post("/reset-password", s.resetPassword)

		r.Group(func(protected chi.Router) {
			protected.Use(s.requireAuth)
			protected.Post("/change-password", s.changePassword)
			protected.Post("/logout-all", s.logoutAll)
		})
	})

	s.router.Post("/funds", s.createFund)
	s.router.Get("/funds/{id}", s.getFund)
	s.router.Post("/funds/{id}/contributions", s.recordContribution)
}

func (s *Server) createFund(w http.ResponseWriter, r *http.Request) {
	var command fundsapp.CreateFundCommand
	if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result, err := s.app.Commands.CreateFund.Handle(command)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) recordContribution(w http.ResponseWriter, r *http.Request) {
	var command fundsapp.RecordContributionCommand
	if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	command.FundID = strings.TrimSpace(chi.URLParam(r, "id"))

	if err := s.app.Commands.RecordContribution.Handle(command); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, fundsapp.ErrFundNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (s *Server) getFund(w http.ResponseWriter, r *http.Request) {
	summary, ok, err := s.app.Queries.GetFundSummary.Handle(fundsapp.GetFundSummaryQuery{
		FundID: strings.TrimSpace(chi.URLParam(r, "id")),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, fundsapp.ErrFundNotFound)
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
