package postgres

import (
	"context"
	"encoding/json"
	"errors"
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
	ID            string          `json:"id"`
	UserID        string          `json:"userId"`
	MonoAccountID string          `json:"monoAccountId"`
	Provider      string          `json:"provider"`
	AccountName   string          `json:"accountName"`
	AccountNumber string          `json:"accountNumber"`
	BankCode      string          `json:"bankCode"`
	BankName      string          `json:"bankName"`
	BankLogoURL   *string         `json:"bankLogoUrl"`
	Currency      string          `json:"currency"`
	Metadata      json.RawMessage `json:"-"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
	LastSyncedAt  *time.Time      `json:"lastSyncedAt"`
	LastEventAt   *time.Time      `json:"-"`
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
		out = append(out, a)
	}
	return out, rows.Err()
}

type MonoTransaction struct {
	ID            string          `json:"id"`
	MonoAccountID string          `json:"monoAccountId"`
	TransactionID string          `json:"transactionId"`
	Amount        int64           `json:"amount"`
	Currency      string          `json:"currency"`
	PaidAt        time.Time       `json:"paidAt"`
	Description   string          `json:"description"`
	Raw           json.RawMessage `json:"raw,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
}

func (r *MonoRepository) ListTransactionsByAccountID(ctx context.Context, internalAccountID string) ([]MonoTransaction, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, mono_account_id, transaction_id, amount, currency, paid_at, description, raw, created_at FROM mono_transactions WHERE mono_account_id = $1 AND EXISTS(SELECT 1 FROM mono_accounts WHERE id=$1 AND deleted_at IS NULL) ORDER BY paid_at DESC`, internalAccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonoTransaction{}
	for rows.Next() {
		var t MonoTransaction
		if err := rows.Scan(&t.ID, &t.MonoAccountID, &t.TransactionID, &t.Amount, &t.Currency, &t.PaidAt, &t.Description, &t.Raw, &t.CreatedAt); err != nil {
			return nil, err
		}
		var signErr error
		t.Amount, signErr = monoapp.SignedAmount(t.Amount, transactionDirection(t.Raw))
		if signErr != nil {
			return nil, signErr
		}
		t.PaidAt = t.PaidAt.UTC()
		t.Raw = nil
		out = append(out, t)
	}
	return out, rows.Err()
}
