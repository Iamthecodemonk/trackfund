package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type MonoRepository struct {
	pool *pgxpool.Pool
}

func NewMonoRepository(pool *pgxpool.Pool) *MonoRepository {
	return &MonoRepository{pool: pool}
}

func (r *MonoRepository) UpsertAccount(ctx context.Context, id, userID, monoAccountID, provider, accountName, accountNumber, bankCode, currency string, metadata map[string]any) error {
	metaJSON, _ := json.Marshal(metadata)
	_, err := r.pool.Exec(ctx, `
        INSERT INTO mono_accounts (id, user_id, mono_account_id, provider, account_name, account_number, bank_code, currency, metadata, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW(),NOW())
        ON CONFLICT (mono_account_id) DO UPDATE SET user_id = EXCLUDED.user_id, provider = EXCLUDED.provider, account_name = EXCLUDED.account_name, account_number = EXCLUDED.account_number, bank_code = EXCLUDED.bank_code, currency = EXCLUDED.currency, metadata = EXCLUDED.metadata, updated_at = NOW()
    `, id, userID, monoAccountID, provider, accountName, accountNumber, bankCode, currency, metaJSON)
	return err
}

func (r *MonoRepository) TransactionExists(ctx context.Context, transactionID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mono_transactions WHERE transaction_id = $1)`, transactionID).Scan(&exists)
	return exists, err
}

func (r *MonoRepository) InsertTransaction(ctx context.Context, id, monoAccountID, transactionID string, amount int64, currency string, paidAt time.Time, description string, raw map[string]any) error {
	rawJSON, _ := json.Marshal(raw)
	_, err := r.pool.Exec(ctx, `
        INSERT INTO mono_transactions (id, mono_account_id, transaction_id, amount, currency, paid_at, description, raw, created_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
    `, id, monoAccountID, transactionID, amount, currency, paidAt, description, rawJSON)
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
	ID            string
	UserID        string
	MonoAccountID string
	Provider      string
	AccountName   string
	AccountNumber string
	BankCode      string
	Currency      string
	Metadata      json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (r *MonoRepository) ListAccounts(ctx context.Context, userID string) ([]MonoAccount, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, user_id, mono_account_id, provider, account_name, account_number, bank_code, currency, metadata, created_at, updated_at FROM mono_accounts WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MonoAccount
	for rows.Next() {
		var a MonoAccount
		if err := rows.Scan(&a.ID, &a.UserID, &a.MonoAccountID, &a.Provider, &a.AccountName, &a.AccountNumber, &a.BankCode, &a.Currency, &a.Metadata, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (r *MonoRepository) GetAccountByID(ctx context.Context, id string) (MonoAccount, bool, error) {
	var a MonoAccount
	err := r.pool.QueryRow(ctx, `SELECT id, user_id, mono_account_id, provider, account_name, account_number, bank_code, currency, metadata, created_at, updated_at FROM mono_accounts WHERE id = $1 LIMIT 1`, id).Scan(&a.ID, &a.UserID, &a.MonoAccountID, &a.Provider, &a.AccountName, &a.AccountNumber, &a.BankCode, &a.Currency, &a.Metadata, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return MonoAccount{}, false, err
	}
	return a, true, nil
}

// ListAllAccounts returns all mono accounts (used by background sync)
func (r *MonoRepository) ListAllAccounts(ctx context.Context) ([]MonoAccount, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, user_id, mono_account_id, provider, account_name, account_number, bank_code, currency, metadata, created_at, updated_at FROM mono_accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MonoAccount
	for rows.Next() {
		var a MonoAccount
		if err := rows.Scan(&a.ID, &a.UserID, &a.MonoAccountID, &a.Provider, &a.AccountName, &a.AccountNumber, &a.BankCode, &a.Currency, &a.Metadata, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

type MonoTransaction struct {
	ID            string
	MonoAccountID string
	TransactionID string
	Amount        int64
	Currency      string
	PaidAt        time.Time
	Description   string
	Raw           json.RawMessage
	CreatedAt     time.Time
}

func (r *MonoRepository) ListTransactionsByAccountID(ctx context.Context, internalAccountID string) ([]MonoTransaction, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, mono_account_id, transaction_id, amount, currency, paid_at, description, raw, created_at FROM mono_transactions WHERE mono_account_id = $1 ORDER BY paid_at DESC`, internalAccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MonoTransaction
	for rows.Next() {
		var t MonoTransaction
		if err := rows.Scan(&t.ID, &t.MonoAccountID, &t.TransactionID, &t.Amount, &t.Currency, &t.PaidAt, &t.Description, &t.Raw, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
