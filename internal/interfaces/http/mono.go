package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	authapp "trackfund/internal/application/auth"
	monoapp "trackfund/internal/application/mono"
	monoclient "trackfund/internal/infrastructure/mono"

	"github.com/go-chi/chi/v5"
)

// createMonoLinkSession builds provider-owned fields from the authenticated user.
func (s *Server) createMonoLinkSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fail := func(status int, code, message string) {
		writeAPIStepError(w, status, "create_link_session", code, message, "")
	}
	if s.app == nil || s.app.MonoClient == nil || strings.TrimSpace(s.app.MonoClient.SecretKey) == "" {
		fail(http.StatusNotImplemented, "mono_not_configured", "MONO_SECRET_KEY is required")
		return
	}
	if s.app.MonoSessions == nil {
		fail(http.StatusServiceUnavailable, "mono_sessions_unavailable", "Mono session storage is unavailable")
		return
	}
	user := currentUser(r)
	if user.ID == "" {
		fail(http.StatusUnauthorized, "unauthorized", "Authentication is required")
		return
	}
	if strings.TrimSpace(user.FullName) == "" || strings.TrimSpace(user.Email) == "" {
		fail(http.StatusUnprocessableEntity, "mono_customer_incomplete", "Your profile requires a name and email")
		return
	}
	var request struct {
		RedirectURL   string `json:"redirect_url"`
		RedirectCamel string `json:"redirectUrl"`
		CallbackURL   string `json:"callback_url"`
		CallbackCamel string `json:"callbackUrl"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&request)
	if err != nil && err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(http.StatusRequestEntityTooLarge, "request_too_large", "Request body exceeds 4 KiB")
		} else {
			fail(http.StatusBadRequest, "invalid_json", "Request body must be a JSON object")
		}
		return
	}
	if err == nil {
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			fail(http.StatusBadRequest, "invalid_json", "Send a single JSON object")
			return
		}
	}
	redirect := ""
	for _, candidate := range []string{request.RedirectURL, request.RedirectCamel, request.CallbackURL, request.CallbackCamel} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if redirect != "" && redirect != candidate {
			fail(http.StatusUnprocessableEntity, "conflicting_callback_urls", "Callback aliases must match")
			return
		}
		redirect = candidate
	}
	if redirect == "" {
		redirect = s.app.Config.MonoCallbackURL
		if redirect == "" {
			redirect = s.app.Config.MonoWebRedirectURL
		}
	}
	if redirect == "" {
		fail(http.StatusUnprocessableEntity, "missing_callback_url", "Configure MONO_CALLBACK_URL/MONO_WEB_REDIRECT_URL or send an allowed redirect_url")
		return
	}
	if !s.app.Config.AllowsMonoRedirect(redirect) {
		fail(http.StatusUnprocessableEntity, "invalid_callback_url", "Callback URL is not allowed")
		return
	}
	reference, err := authapp.RandomID()
	if err != nil {
		fail(http.StatusInternalServerError, "mono_reference_failed", "Could not create a session reference")
		return
	}
	// Save first: a webhook can arrive before the upstream HTTP response.
	if err := s.app.MonoSessions.CreateLinkSession(r.Context(), reference, user.ID, redirect); err != nil {
		fail(http.StatusServiceUnavailable, "mono_session_save_failed", "Could not save the connection session")
		return
	}
	payload := map[string]any{
		"customer": map[string]any{"name": user.FullName, "email": user.Email},
		"scope":    "auth", "redirect_url": redirect, "meta": map[string]any{"ref": reference},
	}
	resp, err := s.app.MonoClient.CreateLinkSession(r.Context(), payload)
	if err != nil {
		if monoclient.IsTimeoutError(err) {
			fail(http.StatusGatewayTimeout, "mono_timeout", "Mono request timed out")
		} else {
			fail(http.StatusBadGateway, "mono_session_failed", "Mono could not create a connection link")
		}
		return
	}
	link, parseErr := url.Parse(resp.Data.MonoURL)
	if parseErr != nil || link.Scheme != "https" || link.Hostname() == "" || link.User != nil {
		fail(http.StatusBadGateway, "invalid_mono_link_response", "Mono did not return a usable hosted link")
		return
	}
	credential := resp.Data.ClientToken
	if credential == "" {
		credential = resp.Data.MonoURL
	}
	writeAPISuccess(w, http.StatusOK, map[string]any{
		"mono_url": resp.Data.MonoURL, "client_token": credential, "reference": reference,
		"status": "pending", "scope": "auth", "redirect_url": redirect,
	})
}

func (s *Server) logMonoInfo(msg string, attrs ...slog.Attr) {
	if s.app != nil && s.app.Logger != nil {
		s.app.Logger.Info(msg, attrs...)
		return
	}
	slog.LogAttrs(context.Background(), slog.LevelInfo, msg, attrs...)
}

func (s *Server) logMonoError(msg string, err error, attrs ...slog.Attr) {
	if s.app != nil && s.app.Logger != nil {
		s.app.Logger.Error(msg, err, attrs...)
		return
	}
	attrs = append(attrs, slog.Any("error", err))
	slog.LogAttrs(context.Background(), slog.LevelError, msg, attrs...)
}

func monoResponseMessage(body string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return "Mono returned an error"
	}
	for _, key := range []string{"message", "error", "detail"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	if nested, ok := payload["error"].(map[string]any); ok {
		for _, key := range []string{"message", "detail", "code"} {
			if value, ok := nested[key].(string); ok && strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return "Mono returned an error"
}

type monoAccountLinkRequest struct {
	Code      string `json:"code"`
	AccountID string `json:"account_id"`
	Token     string `json:"token"`
}

// completeMonoLink exchanges a code from the frontend and persists returned accounts
func (s *Server) completeMonoLink(w http.ResponseWriter, r *http.Request) {
	s.completeMonoAccountLink(w, r, "mono.complete_link")
}

// createMonoAccount accepts the Flutter POST /api/mono/accounts call and completes a Mono link.
func (s *Server) createMonoAccount(w http.ResponseWriter, r *http.Request) {
	s.completeMonoAccountLink(w, r, "mono.accounts.create")
}

func (s *Server) completeMonoAccountLink(w http.ResponseWriter, r *http.Request, logName string) {
	ctx := r.Context()
	if s.app == nil || s.app.MonoClient == nil || s.app.MonoRepo == nil {
		writeAPIStepError(w, http.StatusNotImplemented, "exchange_code", "mono_not_configured", "Mono integration is not configured", "")
		return
	}
	if strings.TrimSpace(s.app.MonoClient.BaseURL) == "" {
		writeAPIStepError(w, http.StatusNotImplemented, "exchange_code", "mono_base_url_missing", "Mono base URL is not configured", "Set MONO_BASE_URL for the current environment.")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.logMonoError(logName+".read_failed", err)
		writeAPIStepError(w, http.StatusBadRequest, "exchange_code", "invalid_request_body", "Could not read request body", err.Error())
		return
	}

	var payload map[string]any
	if len(strings.TrimSpace(string(body))) == 0 {
		payload = map[string]any{}
	} else if err := json.Unmarshal(body, &payload); err != nil {
		s.logMonoError(logName+".invalid_json", err)
		writeAPIStepError(w, http.StatusBadRequest, "exchange_code", "invalid_json", "Request body must be valid JSON", err.Error())
		return
	}

	var req monoAccountLinkRequest
	if err := json.Unmarshal(body, &req); err != nil && len(strings.TrimSpace(string(body))) > 0 {
		s.logMonoError(logName+".mapping_failed", err)
		writeAPIStepError(w, http.StatusBadRequest, "exchange_code", "request_mapping_error", "Could not map Mono account request", err.Error())
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	req.AccountID = strings.TrimSpace(req.AccountID)
	req.Token = strings.TrimSpace(req.Token)
	if req.Code == "" {
		req.Code = firstString(payload, "authorization_code", "authorizationCode", "auth_code", "authCode")
	}
	if req.AccountID == "" {
		req.AccountID = firstString(payload, "accountId", "mono_account_id", "monoAccountId")
	}
	if req.Token == "" {
		req.Token = firstString(payload, "client_token", "clientToken", "access_token", "accessToken")
	}

	s.logMonoInfo(logName+".incoming",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
	)
	s.logMonoInfo(logName+".required_parameters",
		slog.Bool("code_present", req.Code != ""),
		slog.Bool("account_id_present", req.AccountID != ""),
		slog.Bool("token_present", req.Token != ""),
	)

	if req.Code == "" {
		details := "Expected JSON body with a non-empty `code` returned from the Mono Link flow."
		if req.AccountID != "" || req.Token != "" {
			details += " This backend endpoint exchanges a Mono authorization code; account_id/token alone are not enough for this call."
		}
		s.logMonoInfo(logName+".validation_failed",
			slog.String("reason", "missing_code"),
			slog.Bool("account_id_present", req.AccountID != ""),
			slog.Bool("token_present", req.Token != ""),
		)
		writeAPIStepError(w, http.StatusBadRequest, "exchange_code", "missing_mono_code", "Missing required Mono authorization code", details)
		return
	}

	cur := currentUser(r)
	if cur.ID == "" {
		s.logMonoInfo(logName+".validation_failed", slog.String("reason", "missing_authenticated_user"))
		writeAPIStepError(w, http.StatusUnauthorized, "exchange_code", "unauthorized", "Authentication is required", "")
		return
	}

	monoURL := s.app.MonoClient.BaseURL + "/accounts/auth"
	s.logMonoInfo(logName+".mono_request",
		slog.String("method", http.MethodPost),
		slog.String("url", monoURL),
	)

	resp, err := s.app.MonoClient.ExchangeCode(ctx, req.Code)
	if err != nil {
		if monoclient.IsTimeoutError(err) {
			s.logMonoError(logName+".mono_timeout", err,
				slog.String("method", http.MethodPost),
				slog.String("url", monoURL),
			)
			writeAPIStepError(w, http.StatusGatewayTimeout, "exchange_code", "mono_timeout", "Mono request timed out", err.Error())
			return
		}
		var monoErr *monoclient.RequestError
		if errors.As(err, &monoErr) {
			s.logMonoError(logName+".mono_error", err,
				slog.String("method", monoErr.Method),
				slog.String("url", monoErr.URL),
				slog.Int("status", monoErr.StatusCode),
			)
			writeAPIStepError(w, monoErr.StatusCode, "exchange_code", "mono_error", monoResponseMessage(monoErr.Body), monoErr.Body)
			return
		}
		var parseErr *monoclient.ResponseParseError
		if errors.As(err, &parseErr) {
			s.logMonoError(logName+".mono_parse_error", err,
				slog.String("method", parseErr.Method),
				slog.String("url", parseErr.URL),
			)
			writeAPIStepError(w, http.StatusBadGateway, "exchange_code", "mono_parse_error", "Could not parse Mono response", parseErr.Body)
			return
		}
		s.logMonoError(logName+".mono_contact_error", err,
			slog.String("method", http.MethodPost),
			slog.String("url", monoURL),
		)
		writeAPIStepError(w, http.StatusBadGateway, "exchange_code", "mono_contact_error", "Could not contact Mono", err.Error())
		return
	}

	s.logMonoInfo(logName+".mono_mapping",
		slog.Bool("account_id_present", resp.Data.Account.ID != ""),
		slog.String("account_id", resp.Data.Account.ID),
	)
	if acct := resp.Data.Account; acct.ID != "" {
		id, _ := authapp.RandomID()
		metadata := map[string]any{
			"id":             acct.ID,
			"provider":       acct.Provider,
			"name":           acct.Name,
			"account_number": acct.AccountNumber,
			"bank_code":      acct.BankCode,
			"currency":       acct.Currency,
		}
		if err := s.app.MonoRepo.UpsertAccount(ctx, id, cur.ID, acct.ID, acct.Provider, acct.Name, acct.AccountNumber, acct.BankCode, acct.Currency, metadata); err != nil {
			s.logMonoError(logName+".persistence_error", err,
				slog.String("mono_account_id", acct.ID),
				slog.String("user_id", cur.ID),
			)
			writeAPIStepError(w, http.StatusInternalServerError, "store_account", "mono_account_save_failed", "Could not save Mono account", err.Error())
			return
		}
		s.logMonoInfo(logName+".response_returned",
			slog.Int("status", http.StatusOK),
			slog.String("mono_account_id", acct.ID),
		)
		writeAPISuccess(w, http.StatusOK, resp)
		return
	}

	s.logMonoInfo(logName+".mapping_failed",
		slog.String("reason", "missing_account_id"),
		slog.Any("response", resp),
	)
	writeAPIStepError(w, http.StatusBadGateway, "map_account", "mono_mapping_error", "Mono response did not contain an account ID", "Expected `data.id` or `data.account.id` in Mono response.")
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// listMonoAccounts returns mono accounts for a given user (query param user_id)
func (s *Server) listMonoAccounts(w http.ResponseWriter, r *http.Request) {
	cur := currentUser(r)
	if cur.ID == "" {
		writeAPIStepError(w, http.StatusUnauthorized, "fetch_accounts", "unauthorized", "Authentication is required", "")
		return
	}
	accounts, err := s.app.MonoRepo.ListAccounts(r.Context(), cur.ID)
	if err != nil {
		s.logMonoError("mono.accounts.list_failed", err, slog.String("user_id", cur.ID))
		writeAPIStepError(w, http.StatusInternalServerError, "fetch_accounts", "mono_accounts_list_failed", "Could not list Mono accounts", err.Error())
		return
	}
	s.logMonoInfo("mono.accounts.list",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("user_id", cur.ID),
		slog.Int("account_count", len(accounts)),
	)
	s.logMonoInfo("mono.accounts.response_returned",
		slog.Int("status", http.StatusOK),
		slog.Int("account_count", len(accounts)),
	)
	writeAPISuccess(w, http.StatusOK, accounts)
}

// getMonoAccountTransactions returns transactions for internal account id
func (s *Server) getMonoAccountTransactions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, nil)
		return
	}
	// ensure the requesting user owns this account
	cur := currentUser(r)
	if cur.ID == "" {
		writeError(w, http.StatusUnauthorized, nil)
		return
	}
	acc, ok, err := s.app.MonoRepo.GetAccountByID(r.Context(), id)
	if err != nil {
		writeAPIError(w, 503, "mono_accounts_unavailable", "Could not load account", "")
		return
	}
	if !ok {
		writeAPIError(w, 404, "account_not_found", "Account was not found", "")
		return
	}
	if acc.UserID != cur.ID {
		writeAPIError(w, 403, "account_forbidden", "Account belongs to another user", "")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var readiness struct {
		Meta monoapp.Meta `json:"meta"`
	}
	if json.Unmarshal(acc.Metadata, &readiness) != nil {
		writeAPIError(w, 503, "mono_data_unavailable", "Could not read account readiness", "")
		return
	}
	if readiness.Meta.DataStatus == "FAILED" {
		writeAPIError(w, 502, "mono_data_failed", "Mono could not retrieve account data", "")
		return
	}
	if readiness.Meta.DataStatus == "DISCONNECTED" {
		writeAPIError(w, 409, "mono_account_disconnected", "The bank account has been disconnected", "")
		return
	}
	if readiness.Meta.DataStatus == "UNAVAILABLE" || (readiness.Meta.DataStatus == "PARTIAL" && !monoapp.TransactionsAvailable(acc.Metadata)) {
		writeAPISuccess(w, 202, map[string]any{"status": "unavailable", "dataStatus": readiness.Meta.DataStatus, "transactions": []any{}})
		return
	}
	if !monoapp.TransactionsAvailable(acc.Metadata) || acc.LastSyncedAt == nil || (acc.LastEventAt != nil && acc.LastSyncedAt.Before(*acc.LastEventAt)) {
		writeAPISuccess(w, http.StatusAccepted, map[string]any{"status": "syncing", "transactions": []any{}})
		return
	}
	txs, err := s.app.MonoRepo.ListTransactionsByAccountID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPISuccess(w, http.StatusOK, txs)
}
