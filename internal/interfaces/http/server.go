package httpapi

import (
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	fundsapp "trackfund/internal/application/funds"
	"trackfund/internal/composition"
	"trackfund/internal/domain/shared"
)

//go:embed openapi.json
var openapiJSON embed.FS

type Server struct {
	app    *composition.App
	router chi.Router
}

func NewServer(app *composition.App) *Server {
	server := &Server{
		app:    app,
		router: chi.NewRouter(),
	}
	server.router.Use(corsMiddleware)
	// attach request logging middleware when available
	if app != nil && app.Logger != nil {
		server.router.Use(app.Logger.RequestMiddleware)
	}
	server.routes()
	return server
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			if requestedHeaders := r.Header.Get("Access-Control-Request-Headers"); requestedHeaders != "" {
				w.Header().Set("Access-Control-Allow-Headers", requestedHeaders)
			} else {
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
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
		r.Post("/google", s.googleAuth)
		r.Post("/refresh-token", s.refreshToken)
		r.Post("/logout", s.logout)
		r.Post("/forgot-password", s.forgotPassword)
		r.Post("/reset-password", s.resetPassword)

		r.Group(func(protected chi.Router) {
			protected.Use(s.requireAuth)
			protected.Post("/change-password", s.changePassword)
			protected.Post("/logout-all", s.logoutAll)
		})

		s.router.Route("/api/mono", func(r chi.Router) {
			// webhook remains public (Mono will call it)
			r.Post("/webhook", s.monoWebhook)

			// protected Mono routes require authenticated user
			r.Group(func(prot chi.Router) {
				prot.Use(s.requireAuth)
				prot.Post("/link-session", s.createMonoLinkSession)
				prot.Get("/link-session/{reference}", s.getMonoLinkStatus)
				prot.Post("/complete-link", s.completeMonoLink)
				prot.Post("/accounts", s.createMonoAccount)
				prot.Get("/accounts", s.listMonoAccounts)
				prot.Delete("/accounts/{id}", s.deleteMonoAccount)
				prot.Get("/accounts/{id}/transactions", s.getMonoAccountTransactions)
			})
		})
	})

	s.router.Group(func(r chi.Router) {
		r.Use(s.requireAuth)
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				next.ServeHTTP(w, r)
			})
		})
		r.Post("/funds", s.createFund)
		r.Get("/funds", s.listFunds)
		r.Get("/funds/{id}", s.getFund)
		r.Post("/funds/{id}/contributions", s.recordContribution)
		r.Post("/goals", s.createGoal)
		r.Get("/goals", s.listGoals)
		r.Get("/goals/{id}", s.getGoal)
		r.Patch("/goals/{id}", s.updateGoal)
		r.Delete("/goals/{id}", s.deleteGoal)
		// Manual records supplement bank-imported transactions; they are never sent to Mono.
		r.Post("/api/expenses", s.createManualExpense)
		r.Get("/api/expenses", s.listManualExpenses)
		r.Patch("/api/expenses/{id}", s.updateManualExpense)
		r.Delete("/api/expenses/{id}", s.deleteManualExpense)
	})
	profileRoutes := s.router.With(s.requireProfileAuth)
	profileRoutes.Get("/api/profile/me", s.getProfile)
	profileRoutes.Patch("/api/profile/me", s.patchProfile)
	profileRoutes.Post("/api/profile/me/email/verify", s.verifyProfileEmail)
	profileRoutes.Post("/api/profile/me/avatar", s.uploadProfileAvatar)
	goals := s.router.With(s.requireAuth)
	goals.Post("/api/goals", s.createGoal)
	goals.Get("/api/goals", s.listGoals)
	goals.Get("/api/goals/{id}", s.getGoal)
	goals.Patch("/api/goals/{id}", s.updateGoal)
	goals.Delete("/api/goals/{id}", s.deleteGoal)
	goals.Patch("/api/transactions/{id}", s.updateTransactionMetadata)
	goals.Get("/api/transactions", s.listTransactions)
	goals.Post("/api/transactions/transfers", s.markTransfer)
	goals.Get("/api/notifications", s.listNotifications)
	goals.Patch("/api/notifications/{id}", s.updateNotification)
	goals.Patch("/api/notifications/{id}/read", s.updateNotification)
	goals.Post("/api/notifications/read-all", s.markAllNotificationsRead)
	s.router.Get("/internal/metrics", s.operationalMetrics)
	// serve OpenAPI and a simple docs page
	s.router.Get("/openapi.json", s.serveOpenAPI)
	s.router.Get("/api.json", s.serveOpenAPI)
	s.router.Get("/docs", s.serveDocs)
}

func (s *Server) serveOpenAPI(w http.ResponseWriter, r *http.Request) {
	data, err := openapiJSON.ReadFile("openapi.json")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) serveDocs(w http.ResponseWriter, r *http.Request) {
	// serve a minimal HTML page that loads the OpenAPI JSON in Swagger UI CDN
	html := `<!doctype html>
<html>
<head>
	<title>TrackFund API Docs</title>
	<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@4/swagger-ui.css" />
</head>
<body>
	<div id="swagger-ui"></div>
	<script src="https://unpkg.com/swagger-ui-dist@4/swagger-ui-bundle.js"></script>
	<script>
		const ui = SwaggerUIBundle({
			url: '/openapi.json',
			dom_id: '#swagger-ui'
		});
	</script>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(html))
}

func (s *Server) createFund(w http.ResponseWriter, r *http.Request) {
	var command fundsapp.CreateFundCommand
	if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	command.UserID = currentUser(r).ID
	result, err := s.app.Commands.CreateFund.Handle(command)
	if err != nil {
		writeFundError(w, err)
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
	command.UserID = currentUser(r).ID
	command.FundID = strings.TrimSpace(chi.URLParam(r, "id"))

	if err := s.app.Commands.RecordContribution.Handle(command); err != nil {
		writeFundError(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (s *Server) getFund(w http.ResponseWriter, r *http.Request) {
	summary, ok, err := s.app.Queries.GetFundSummary.Handle(fundsapp.GetFundSummaryQuery{
		FundID: strings.TrimSpace(chi.URLParam(r, "id")),
		UserID: currentUser(r).ID,
	})
	if err != nil {
		writeFundError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, fundsapp.ErrFundNotFound)
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) listFunds(w http.ResponseWriter, r *http.Request) {
	page, limit := 1, 20
	var err error
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			writeAPIError(w, http.StatusBadRequest, "invalid_pagination", "page must be a positive integer", "")
			return
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			writeAPIError(w, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 100", "")
			return
		}
	}

	result, err := s.app.Queries.ListFunds.Handle(fundsapp.ListFundsQuery{
		UserID: currentUser(r).ID,
		Page:   page,
		Limit:  limit,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "fund_list_failed", "Could not list funds", "")
		return
	}
	writeAPISuccess(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	message := http.StatusText(status)
	if message == "" {
		message = "error"
	}
	if err != nil {
		message = err.Error()
	}
	writeAPIError(w, status, strings.ToLower(strings.ReplaceAll(http.StatusText(status), " ", "_")), message, "")
}

func writeAPISuccess(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{
		"success": true,
		"data":    data,
	})
}

func writeAPIError(w http.ResponseWriter, status int, code, message, details string) {
	writeAPIStepError(w, status, code, code, message, details)
}

func writeAPIStepError(w http.ResponseWriter, status int, step, code, message, details string) {
	if code == "" {
		code = "error"
	}
	if step == "" {
		step = code
	}
	if message == "" {
		message = http.StatusText(status)
	}
	body := map[string]any{
		"success": false,
		"step":    step,
		"status":  status,
		"message": message,
		"details": details,
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	}
	writeJSON(w, status, body)
}

func writeFundError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fundsapp.ErrFundAuthenticationRequired):
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", err.Error(), "")
	case errors.Is(err, fundsapp.ErrFundForbidden):
		writeAPIError(w, http.StatusForbidden, "fund_forbidden", err.Error(), "")
	case errors.Is(err, fundsapp.ErrFundNotFound):
		writeAPIError(w, http.StatusNotFound, "fund_not_found", err.Error(), "")
	case errors.Is(err, shared.ErrNameRequired), errors.Is(err, shared.ErrTargetAmountInvalid), errors.Is(err, shared.ErrCurrencyInvalid), errors.Is(err, shared.ErrContributionInvalid), errors.Is(err, shared.ErrContributorRequired):
		writeAPIError(w, http.StatusBadRequest, "invalid_fund", err.Error(), "")
	default:
		writeAPIError(w, http.StatusInternalServerError, "fund_operation_failed", "Could not complete the fund operation", "")
	}
}
