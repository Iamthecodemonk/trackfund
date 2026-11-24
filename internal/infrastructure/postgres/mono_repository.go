package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	monoapp "trackfund/internal/application/mono"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MonoRepository struct {
	pool *pgxpool.Pool
}

func NewMonoRepository(pool *pgxpool.Pool) *MonoRepository {
	return &MonoRepository{pool: pool}
}

func (r *MonoRepository) UpsertAccount(ctx context.Context, id, userID, monoAccountID, provider, accountName, accountNumber, bankCode, currency string, metadata map[string]any) error {
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `
        INSERT INTO mono_accounts (id, user_id, mono_account_id, provider, account_name, account_number, bank_code, currency, metadata, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW(),NOW())
        ON CONFLICT (mono_account_id) DO UPDATE SET provider = EXCLUDED.provider, account_name = EXCLUDED.account_name, account_number = EXCLUDED.account_number, bank_code = EXCLUDED.bank_code, currency = EXCLUDED.currency, metadata = COALESCE(mono_accounts.metadata,'{}'::jsonb)||EXCLUDED.metadata, updated_at = NOW() WHERE mono_accounts.user_id=EXCLUDED.user_id AND mono_accounts.deleted_at IS NULL
    `, id, userID, monoAccountID, provider, accountName, accountNumber, bankCode, currency, metaJSON)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return monoapp.ErrOwnershipConflict
	}
	return nil
}

func (r *MonoRepository) TransactionExists(ctx context.Context, transactionID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mono_transactions WHERE transaction_id = $1)`, transactionID).Scan(&exists)
	return exists, err
}

func (r *MonoRepository) InsertTransaction(ctx context.Context, id, monoAccountID, transactionID string, amount int64, currency string, paidAt time.Time, description string, raw map[string]any) error {
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var accountID string
	err = r.pool.QueryRow(ctx, `
        INSERT INTO mono_transactions (id, mono_account_id, transaction_id, amount, currency, paid_at, description, raw, created_at)
        SELECT $1,$2,$3,$4,$5,$6,$7,$8,NOW() FROM mono_accounts WHERE id=$2 AND deleted_at IS NULL FOR SHARE
 ON CONFLICT(transaction_id) DO UPDATE SET transaction_id=EXCLUDED.transaction_id WHERE mono_transactions.mono_account_id=EXCLUDED.mono_account_id RETURNING mono_account_id
    `, id, monoAccountID, transactionID, amount, currency, paidAt, description, rawJSON).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		var active bool
		if queryErr := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mono_accounts WHERE id=$1 AND deleted_at IS NULL)`, monoAccountID).Scan(&active); queryErr != nil {
			return queryErr
		}
		if !active {
			return nil
		}
		return monoapp.ErrOwnershipConflict
	}
	return err
}

// GetInternalAccountIDByMonoID returns the internal mono_accounts.id for the given mono_account_id
func (r *MonoRepository) GetInternalAccountIDByMonoID(ctx context.Context, monoAccountID string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `SELECT id FROM mono_accounts WHERE mono_account_id = $1 LIMIT 1`, monoAccountID).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

type MonoAccount struct {
	ID            string  `json:"id"`
	UserID        string  `json:"userId"`
	MonoAccountID string  `json:"monoAccountId"`
	Provider      string  `json:"provider"`
	AccountName   string  `json:"accountName"`
	AccountNumber string  `json:"accountNumber"`
	BankCode      string  `json:"bankCode"`
	BankName      string  `json:"bankName"`
	BankLogoURL   *string `json:"bankLogoUrl"`
	Currency      string  `json:"currency"`
	// Balance is Mono's latest reported account balance in integer minor units.
	// It is nil until Mono confirms balance data for this connection.
	Balance            *int64          `json:"balance"`
	Metadata           json.RawMessage `json:"-"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
	LastSyncedAt       *time.Time      `json:"lastSyncedAt"`
	LastEventAt        *time.Time      `json:"-"`
	BalanceObservedAt  *time.Time      `json:"balanceObservedAt"`
	DataStatus         string          `json:"dataStatus"`
	SyncStatus         string          `json:"syncStatus"`
	InitialImportReady bool            `json:"initialImportReady"`
	ConnectionStatus   string          `json:"connectionStatus"`
	FailureReason      *string         `json:"failureReason,omitempty"`
	HistoricalCoverage string          `json:"historicalCoverage"`
}

func (r *MonoRepository) ListAccounts(ctx context.Context, userID string) ([]MonoAccount, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, user_id, mono_account_id, provider, account_name, COALESCE(account_number,''), COALESCE(bank_code,''), COALESCE(currency,''), metadata, created_at, updated_at,last_synced_at,last_event_at FROM mono_accounts WHERE user_id = $1 AND deleted_at IS NULL`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonoAccount{}
	for rows.Next() {
		var a MonoAccount
		if err := rows.Scan(&a.ID, &a.UserID, &a.MonoAccountID, &a.Provider, &a.AccountName, &a.AccountNumber, &a.BankCode, &a.Currency, &a.Metadata, &a.CreatedAt, &a.UpdatedAt, &a.LastSyncedAt, &a.LastEventAt); err != nil {
			return nil, err
		}
		a.applyBranding()
		a.applyBalance()
		a.applySyncStatus()
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *MonoRepository) GetAccountByID(ctx context.Context, id string) (MonoAccount, bool, error) {
	var a MonoAccount
	err := r.pool.QueryRow(ctx, `SELECT id, user_id, mono_account_id, provider, account_name, COALESCE(account_number,''), COALESCE(bank_code,''), COALESCE(currency,''), metadata, created_at, updated_at,last_synced_at,last_event_at FROM mono_accounts WHERE id = $1 AND deleted_at IS NULL LIMIT 1`, id).Scan(&a.ID, &a.UserID, &a.MonoAccountID, &a.Provider, &a.AccountName, &a.AccountNumber, &a.BankCode, &a.Currency, &a.Metadata, &a.CreatedAt, &a.UpdatedAt, &a.LastSyncedAt, &a.LastEventAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MonoAccount{}, false, nil
	}
	if err != nil {
		return MonoAccount{}, false, err
	}
	a.applyBranding()
	a.applyBalance()
	a.applySyncStatus()
	return a, true, nil
}

// ListAllAccounts returns all mono accounts (used by background sync)
func (r *MonoRepository) ListAllAccounts(ctx context.Context) ([]MonoAccount, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, user_id, mono_account_id, provider, account_name, COALESCE(account_number,''), COALESCE(bank_code,''), COALESCE(currency,''), metadata, created_at, updated_at,last_synced_at,last_event_at FROM mono_accounts WHERE deleted_at IS NULL AND (metadata->'meta'->>'data_status'='AVAILABLE' OR (metadata->'meta'->>'data_status'='PARTIAL' AND metadata->'meta'->'retrieved_data' ? 'transactions')) AND (last_synced_at IS NULL OR last_synced_at<last_event_at OR last_synced_at<NOW()-INTERVAL '5 minutes')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonoAccount{}
	for rows.Next() {
		var a MonoAccount
		if err := rows.Scan(&a.ID, &a.UserID, &a.MonoAccountID, &a.Provider, &a.AccountName, &a.AccountNumber, &a.BankCode, &a.Currency, &a.Metadata, &a.CreatedAt, &a.UpdatedAt, &a.LastSyncedAt, &a.LastEventAt); err != nil {
			return nil, err
		}
		a.applyBranding()
		a.applyBalance()
		a.applySyncStatus()
		out = append(out, a)
	}
	return out, rows.Err()
}

func (a *MonoAccount) applyBalance() {
	// Webhook snapshots keep the provider's balance as raw JSON to prevent an
	// intermediate float conversion from losing money precision.
	var snapshot struct {
		Account struct {
			Balance json.RawMessage `json:"balance"`
		} `json:"account"`
		Balance json.RawMessage `json:"balance"`
	}
	if json.Unmarshal(a.Metadata, &snapshot) != nil {
		return
	}
	raw := snapshot.Account.Balance
	if len(raw) == 0 {
		raw = snapshot.Balance // Compatibility for older code-completion snapshots.
	}
	if balance, ok := parseIntegerBalance(raw); ok {
		a.Balance = &balance
	}
}

func (a *MonoAccount) applySyncStatus() {
	var snapshot struct {
		Meta struct {
			DataStatus string `json:"data_status"`
		} `json:"meta"`
	}
	_ = json.Unmarshal(a.Metadata, &snapshot)
	a.DataStatus = strings.ToUpper(strings.TrimSpace(snapshot.Meta.DataStatus))
	if a.DataStatus == "" {
		a.DataStatus = "PROCESSING"
	}
	a.HistoricalCoverage = "unknown"
	if a.LastEventAt != nil {
		observed := a.LastEventAt.UTC()
		a.BalanceObservedAt = &observed
	}
	a.ConnectionStatus = "connected"
	a.SyncStatus = "syncing"
	switch a.DataStatus {
	case "FAILED":
		a.ConnectionStatus = "failed"
		a.SyncStatus = "failed"
		reason := "provider_data_failed"
		a.FailureReason = &reason
	case "DISCONNECTED":
		a.ConnectionStatus = "reconnection_required"
		a.SyncStatus = "unavailable"
		reason := "provider_disconnected"
		a.FailureReason = &reason
	case "UNAVAILABLE":
		a.SyncStatus = "unavailable"
	default:
		if a.LastSyncedAt != nil {
			a.InitialImportReady = true
			a.SyncStatus = "ready"
		}
		if a.LastSyncedAt != nil && a.LastEventAt != nil && a.LastSyncedAt.Before(*a.LastEventAt) {
			a.SyncStatus = "syncing"
		}
	}
}

func parseIntegerBalance(raw json.RawMessage) (int64, bool) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return 0, false
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, false
		}
	}
	balance, err := strconv.ParseInt(value, 10, 64)
	return balance, err == nil
}

type MonoTransaction struct {
	ID                   string          `json:"id"`
	MonoAccountID        string          `json:"monoAccountId"`
	TransactionID        string          `json:"transactionId"`
	Amount               int64           `json:"amount"`
	Currency             string          `json:"currency"`
	PaidAt               time.Time       `json:"paidAt"`
	Description          string          `json:"description"`
	OriginalDescription  string          `json:"originalDescription"`
	UserDescription      *string         `json:"userDescription,omitempty"`
	PersonalNote         *string         `json:"personalNote,omitempty"`
	Category             *string         `json:"category,omitempty"`
	CategoryID           *string         `json:"categoryId,omitempty"`
	Source               string          `json:"source"`
	Direction            string          `json:"direction"`
	TransferGroupID      *string         `json:"transferGroupId,omitempty"`
	CountsTowardSpending bool            `json:"countsTowardSpending"`
	CountsTowardIncome   bool            `json:"countsTowardIncome"`
	Raw                  json.RawMessage `json:"raw,omitempty"`
	CreatedAt            time.Time       `json:"createdAt"`
}

func (r *MonoRepository) ListTransactionsByAccountID(ctx context.Context, internalAccountID string) ([]MonoTransaction, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, mono_account_id, transaction_id, amount, currency, paid_at, COALESCE(description,''), raw, created_at,category,category_id,user_description,personal_note,transfer_group_id,counts_toward_spending,counts_toward_income FROM mono_transactions WHERE mono_account_id = $1 AND EXISTS(SELECT 1 FROM mono_accounts WHERE id=$1 AND deleted_at IS NULL) ORDER BY paid_at DESC,id DESC`, internalAccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonoTransaction{}
	for rows.Next() {
		var t MonoTransaction
		if err := rows.Scan(&t.ID, &t.MonoAccountID, &t.TransactionID, &t.Amount, &t.Currency, &t.PaidAt, &t.OriginalDescription, &t.Raw, &t.CreatedAt, &t.Category, &t.CategoryID, &t.UserDescription, &t.PersonalNote, &t.TransferGroupID, &t.CountsTowardSpending, &t.CountsTowardIncome); err != nil {
			return nil, err
		}
		if err := presentTransaction(&t); err != nil {
			return nil, err
		}
		t.Raw = nil
		out = append(out, t)
	}
	return out, rows.Err()
}

type TransactionHistoryPage struct {
	Transactions []MonoTransaction   `json:"transactions"`
	Pagination   Page                `json:"pagination"`
	Coverage     TransactionCoverage `json:"coverage"`
}
type TransactionCoverage struct {
	From     *time.Time `json:"from,omitempty"`
	To       *time.Time `json:"to,omitempty"`
	Complete bool       `json:"complete"`
	Note     string     `json:"note"`
}

func (r *MonoRepository) ListTransactions(ctx context.Context, userID string, from, to *time.Time, limit, offset, page int) (TransactionHistoryPage, error) {
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM mono_transactions t JOIN mono_accounts a ON a.id=t.mono_account_id WHERE a.user_id=$1 AND a.deleted_at IS NULL AND ($2::timestamptz IS NULL OR t.paid_at >= $2) AND ($3::timestamptz IS NULL OR t.paid_at < $3)`, userID, from, to).Scan(&total); err != nil {
		return TransactionHistoryPage{}, err
	}
	rows, err := r.pool.Query(ctx, `SELECT t.id,t.mono_account_id,t.transaction_id,t.amount,t.currency,t.paid_at,COALESCE(t.description,''),t.raw,t.created_at,t.category,t.category_id,t.user_description,t.personal_note,t.transfer_group_id,t.counts_toward_spending,t.counts_toward_income FROM mono_transactions t JOIN mono_accounts a ON a.id=t.mono_account_id WHERE a.user_id=$1 AND a.deleted_at IS NULL AND ($2::timestamptz IS NULL OR t.paid_at >= $2) AND ($3::timestamptz IS NULL OR t.paid_at < $3) ORDER BY t.paid_at DESC,t.id DESC LIMIT $4 OFFSET $5`, userID, from, to, limit, offset)
	if err != nil {
		return TransactionHistoryPage{}, err
	}
	defer rows.Close()
	items := []MonoTransaction{}
	for rows.Next() {
		var t MonoTransaction
		if err := rows.Scan(&t.ID, &t.MonoAccountID, &t.TransactionID, &t.Amount, &t.Currency, &t.PaidAt, &t.OriginalDescription, &t.Raw, &t.CreatedAt, &t.Category, &t.CategoryID, &t.UserDescription, &t.PersonalNote, &t.TransferGroupID, &t.CountsTowardSpending, &t.CountsTowardIncome); err != nil {
			return TransactionHistoryPage{}, err
		}
		if err := presentTransaction(&t); err != nil {
			return TransactionHistoryPage{}, err
		}
		t.Raw = nil
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return TransactionHistoryPage{}, err
	}
	return TransactionHistoryPage{Transactions: items, Pagination: Page{Page: page, Limit: limit, Total: total, HasMore: page*limit < total}, Coverage: TransactionCoverage{From: from, To: to, Complete: false, Note: "Provider history coverage is not yet guaranteed; this is a stable page of imported transactions."}}, nil
}

func presentTransaction(t *MonoTransaction) error {
	direction := strings.ToLower(transactionDirection(t.Raw))
	amount, err := monoapp.SignedAmount(t.Amount, direction)
	if err != nil {
		return err
	}
	t.Amount = amount
	t.PaidAt = t.PaidAt.UTC()
	t.Description = t.OriginalDescription
	if t.UserDescription != nil {
		t.Description = *t.UserDescription
	}
	t.Source = "bank"
	t.Direction = direction
	if t.Direction == "" {
		if t.Amount < 0 {
			t.Direction = "debit"
		} else if t.Amount > 0 {
			t.Direction = "credit"
		} else {
			t.Direction = "unknown"
		}
	}
	return nil
}

type TransactionUserMetadata struct{ Category, CategoryID, UserDescription, PersonalNote *string }

func (r *MonoRepository) UpdateTransactionMetadata(ctx context.Context, userID, transactionID string, in TransactionUserMetadata) (MonoTransaction, error) {
	var accountID string
	err := r.pool.QueryRow(ctx, `UPDATE mono_transactions t SET category=CASE WHEN $3 THEN NULLIF($4,'') ELSE t.category END,category_id=CASE WHEN $5 THEN NULLIF($6,'') ELSE t.category_id END,user_description=CASE WHEN $7 THEN NULLIF($8,'') ELSE t.user_description END,personal_note=CASE WHEN $9 THEN NULLIF($10,'') ELSE t.personal_note END FROM mono_accounts a WHERE t.id=$1 AND a.id=t.mono_account_id AND a.user_id=$2 AND a.deleted_at IS NULL RETURNING t.mono_account_id`, transactionID, userID, in.Category != nil, stringValue(in.Category), in.CategoryID != nil, stringValue(in.CategoryID), in.UserDescription != nil, stringValue(in.UserDescription), in.PersonalNote != nil, stringValue(in.PersonalNote)).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return MonoTransaction{}, pgx.ErrNoRows
	}
	if err != nil {
		return MonoTransaction{}, err
	}
	items, err := r.ListTransactionsByAccountID(ctx, accountID)
	if err != nil {
		return MonoTransaction{}, err
	}
	for _, item := range items {
		if item.ID == transactionID {
			return item, nil
		}
	}
	return MonoTransaction{}, pgx.ErrNoRows
}
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
