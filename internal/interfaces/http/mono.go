package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	authapp "trackfund/internal/application/auth"
	monoclient "trackfund/internal/infrastructure/mono"

	"github.com/go-chi/chi/v5"
)

// monoWebhook receives webhook events from Mono, verifies signature, and persists accounts/transactions.
func (s *Server) monoWebhook(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("MONO_WEBHOOK_SECRET")
	if secret == "" {
		http.Error(w, "webhook secret not configured", http.StatusInternalServerError)
		return
	}
	// Mono sends the webhook secret in the `mono-webhook-secret` header for verification
	hdr := r.Header.Get("mono-webhook-secret")
	if hdr == "" {
		http.Error(w, "missing webhook secret header", http.StatusUnauthorized)
		return
	}
	if hdr != secret {
		http.Error(w, "invalid webhook secret", http.StatusUnauthorized)
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	// simple event dispatcher based on `event` field per Mono webhook format
	typ, _ := payload["event"].(string)
	data, _ := payload["data"].(map[string]any)
	ctx := r.Context()

	if s.app == nil || s.app.MonoRepo == nil {
		http.Error(w, "mono repository not configured", http.StatusInternalServerError)
		return
	}

	switch typ {
	case "mono.events.account_connected", "mono.events.account_updated", "mono.events.account_created":
		// expect data.account
		if acct, ok := data["account"].(map[string]any); ok {
			// generate an internal id
			id, _ := authapp.RandomID()
			userID, _ := acct["user_id"].(string)
			monoAccountID, _ := acct["id"].(string)
			provider, _ := acct["provider"].(string)
			accountName, _ := acct["name"].(string)
			accountNumber, _ := acct["account_number"].(string)
			bankCode, _ := acct["bank_code"].(string)
			currency, _ := acct["currency"].(string)
			// metadata: keep full account object
			_ = s.app.MonoRepo.UpsertAccount(ctx, id, userID, monoAccountID, provider, accountName, accountNumber, bankCode, currency, acct)
		}
	case "mono.events.transactions_created", "mono.events.transaction_created", "mono.events.transactions.created":
		// data.transactions is expected
		if arr, ok := data["transactions"].([]any); ok {
			for _, v := range arr {
				if tx, ok := v.(map[string]any); ok {
					transactionID, _ := tx["id"].(string)
					exists, err := s.app.MonoRepo.TransactionExists(ctx, transactionID)
					if err != nil {
						// log and continue
						continue
					}
					if exists {
						continue
					}
					// find mono_account id mapping: Mono provides account id in tx.account_id; need to query mono_accounts.id
					monoAccountIDRaw, _ := tx["account_id"].(string)
					internalAccountID, err := s.app.MonoRepo.GetInternalAccountIDByMonoID(ctx, monoAccountIDRaw)
					if err != nil || internalAccountID == "" {
						// skip if unknown account
						continue
					}
					amountFloat, _ := tx["amount"].(float64)
					amount := int64(amountFloat)
					currency, _ := tx["currency"].(string)
					paidAtStr, _ := tx["paid_at"].(string)
					var paidAt time.Time
					if paidAtStr != "" {
						paidAt, _ = time.Parse(time.RFC3339, paidAtStr)
					}
					description, _ := tx["description"].(string)
					id, _ := authapp.RandomID()
					_ = s.app.MonoRepo.InsertTransaction(ctx, id, internalAccountID, transactionID, amount, currency, paidAt, description, tx)
				}
			}
		}
	default:
		// ignore unknown events
	}

	w.WriteHeader(http.StatusOK)
}

// createMonoLinkSession starts a Mono connect session (forwarding request body to Mono client)
func (s *Server) createMonoLinkSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.app == nil || s.app.MonoClient == nil || s.app.MonoClient.SecretKey == "" {
		writeAPIStepError(w, http.StatusNotImplemented, "create_link_session", "mono_not_configured", "Mono integration is not configured", "MONO_SECRET_KEY is required.")
		return
	}
	if strings.TrimSpace(s.app.MonoClient.BaseURL) == "" {
		writeAPIStepError(w, http.StatusNotImplemented, "create_link_session", "mono_base_url_missing", "Mono base URL is not configured", "Set MONO_BASE_URL for the current environment, for example the Mono sandbox URL in development and live URL in production.")
		return
	}
	payload := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && err != io.EOF {
		s.logMonoError("mono.link_session.invalid_json", err)
		writeAPIStepError(w, http.StatusBadRequest, "create_link_session", "invalid_json", "Request body must be valid JSON", err.Error())
		return
	}
	if redirectURL, ok := payload["redirectUrl"].(string); ok && redirectURL != "" {
		payload["redirect_url"] = redirectURL
		delete(payload, "redirectUrl")
	}
	if callbackURL, ok := payload["callback_url"].(string); ok && callbackURL != "" {
		payload["redirect_url"] = callbackURL
	}
	if callbackURL, ok := payload["callbackUrl"].(string); ok && callbackURL != "" {
		payload["redirect_url"] = callbackURL
	}
	if redirectURL, _ := payload["redirect_url"].(string); strings.TrimSpace(redirectURL) == "" {
		if s.app != nil && strings.TrimSpace(s.app.Config.MonoCallbackURL) != "" {
			payload["redirect_url"] = strings.TrimSpace(s.app.Config.MonoCallbackURL)
		}
	}
	if redirectURL, _ := payload["redirect_url"].(string); strings.TrimSpace(redirectURL) == "" {
		s.logMonoInfo("mono.link_session.validation_failed", slog.String("reason", "missing_redirect_url"))
		writeAPIStepError(w, http.StatusBadRequest, "create_link_session", "missing_callback_url", "Missing Mono callback URL", "Send redirect_url/callback_url from Flutter or set MONO_CALLBACK_URL for this environment.")
		return
	}
	if scope, ok := payload["scope"].(string); !ok || scope == "" {
		payload["scope"] = "auth"
	}
	s.logMonoInfo("mono.link_session.incoming",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("payload", payload),
	)
	s.logMonoInfo("mono.link_session.mono_request",
		slog.String("method", http.MethodPost),
		slog.String("url", s.app.MonoClient.BaseURL+"/accounts/initiate"),
		slog.Bool("secret_key_configured", s.app.MonoClient.SecretKey != ""),
		slog.Bool("public_key_configured", s.app.MonoClient.PublicKey != ""),
		slog.Any("payload", payload),
	)
	resp, err := s.app.MonoClient.CreateLinkSession(ctx, payload)
	if err != nil {
		if monoclient.IsTimeoutError(err) {
			s.logMonoError("mono.link_session.timeout", err)
			writeAPIStepError(w, http.StatusGatewayTimeout, "create_link_session", "mono_timeout", "Mono request timed out", err.Error())
			return
		}
		if errors.Is(err, monoclient.ErrInvalidLinkSessionResponse) {
			s.logMonoError("mono.link_session.invalid_response", err)
			writeAPIStepError(w, http.StatusBadGateway, "create_link_session", "invalid_mono_link_response", "Mono did not return a usable link session credential", "Expected mono_url or client_token in Mono response.")
			return
		}
		var monoErr *monoclient.RequestError
		if errors.As(err, &monoErr) {
			s.logMonoError("mono.link_session.mono_error", err,
				slog.String("method", monoErr.Method),
				slog.String("url", monoErr.URL),
				slog.Int("status", monoErr.StatusCode),
				slog.String("raw_body", monoErr.Body),
			)
			writeAPIStepError(w, monoErr.StatusCode, "create_link_session", "mono_error", monoResponseMessage(monoErr.Body), monoErr.Body)
			return
		}
		var parseErr *monoclient.ResponseParseError
		if errors.As(err, &parseErr) {
			s.logMonoError("mono.link_session.parse_error", err,
				slog.String("method", parseErr.Method),
				slog.String("url", parseErr.URL),
				slog.String("raw_body", parseErr.Body),
			)
			writeAPIStepError(w, http.StatusBadGateway, "create_link_session", "mono_parse_error", "Could not parse Mono link session response", parseErr.Body)
			return
		}
		s.logMonoError("mono.link_session.contact_error", err)
		writeAPIStepError(w, http.StatusBadGateway, "create_link_session", "mono_contact_error", "Could not contact Mono", err.Error())
		return
	}
	linkCredential := resp.Data.ClientToken
	if linkCredential == "" {
		linkCredential = resp.Data.MonoURL
	}
	if strings.TrimSpace(linkCredential) == "" {
		s.logMonoInfo("mono.link_session.parsed_response",
			slog.Bool("client_token_present", resp.Data.ClientToken != ""),
			slog.Bool("mono_url_present", resp.Data.MonoURL != ""),
		)
		writeAPIStepError(w, http.StatusBadGateway, "create_link_session", "empty_link_credential", "Mono link session response was missing client_token/mono_url", "Backend refused to return HTTP 200 with an empty link credential.")
		return
	}
	data := map[string]any{
		"client_token": linkCredential,
		"mono_url":     resp.Data.MonoURL,
		"scope":        resp.Data.Scope,
		"redirect_url": resp.Data.RedirectURL,
		"is_multi":     resp.Data.IsMulti,
		"created_at":   resp.Data.CreatedAt,
	}
	s.logMonoInfo("mono.link_session.parsed_response",
		slog.Bool("client_token_present", resp.Data.ClientToken != ""),
		slog.Bool("mono_url_present", resp.Data.MonoURL != ""),
		slog.String("redirect_url", resp.Data.RedirectURL),
	)
	s.logMonoInfo("mono.link_session.response_returned",
		slog.Int("status", http.StatusOK),
		slog.Any("data", data),
	)
	writeAPISuccess(w, http.StatusOK, data)
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
		s.logMonoError(logName+".invalid_json", err, slog.String("raw_payload", string(body)))
		writeAPIStepError(w, http.StatusBadRequest, "exchange_code", "invalid_json", "Request body must be valid JSON", err.Error())
		return
	}

	var req monoAccountLinkRequest
	if err := json.Unmarshal(body, &req); err != nil && len(strings.TrimSpace(string(body))) > 0 {
		s.logMonoError(logName+".mapping_failed", err, slog.Any("payload", payload))
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
		slog.Any("payload", payload),
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
		slog.Any("payload", map[string]any{"code": req.Code}),
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
				slog.String("raw_body", monoErr.Body),
			)
			writeAPIStepError(w, monoErr.StatusCode, "exchange_code", "mono_error", monoResponseMessage(monoErr.Body), monoErr.Body)
			return
		}
		var parseErr *monoclient.ResponseParseError
		if errors.As(err, &parseErr) {
			s.logMonoError(logName+".mono_parse_error", err,
				slog.String("method", parseErr.Method),
				slog.String("url", parseErr.URL),
				slog.String("raw_body", parseErr.Body),
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
	if err != nil || !ok || acc.UserID != cur.ID {
		writeError(w, http.StatusUnauthorized, nil)
		return
	}
	txs, err := s.app.MonoRepo.ListTransactionsByAccountID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, txs)
}
