package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	authapp "trackfund/internal/application/auth"

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
	if s.app == nil || s.app.MonoClient == nil || s.app.MonoClient.PublicKey == "" {
		writeError(w, http.StatusNotImplemented, nil)
		return
	}
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	resp, err := s.app.MonoClient.CreateLinkSession(ctx, payload)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// completeMonoLink exchanges a code from the frontend and persists returned accounts
func (s *Server) completeMonoLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.app == nil || s.app.MonoClient == nil || s.app.MonoRepo == nil {
		writeError(w, http.StatusNotImplemented, nil)
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// get authenticated user id from context
	cur := currentUser(r)
	if cur.ID == "" {
		writeError(w, http.StatusUnauthorized, nil)
		return
	}
	resp, err := s.app.MonoClient.ExchangeCode(ctx, req.Code)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
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
		_ = s.app.MonoRepo.UpsertAccount(ctx, id, cur.ID, acct.ID, acct.Provider, acct.Name, acct.AccountNumber, acct.BankCode, acct.Currency, metadata)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": resp})
}

// listMonoAccounts returns mono accounts for a given user (query param user_id)
func (s *Server) listMonoAccounts(w http.ResponseWriter, r *http.Request) {
	cur := currentUser(r)
	if cur.ID == "" {
		writeError(w, http.StatusUnauthorized, nil)
		return
	}
	accounts, err := s.app.MonoRepo.ListAccounts(r.Context(), cur.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, accounts)
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
