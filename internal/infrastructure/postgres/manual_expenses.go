package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrManualExpenseNotFound            = errors.New("manual expense not found")
	ErrInvalidManualExpense             = errors.New("invalid manual expense")
	ErrManualExpenseIdempotencyConflict = errors.New("manual expense idempotency key belongs to a deleted expense")
)

type ManualExpenseRepository struct{ pool *pgxpool.Pool }

func NewManualExpenseRepository(pool *pgxpool.Pool) *ManualExpenseRepository {
	return &ManualExpenseRepository{pool: pool}
}

type ManualExpense struct {
	ID            string    `json:"id"`
	Source        string    `json:"source"`
	Amount        int64     `json:"amount"`
	Direction     string    `json:"direction"`
	Currency      string    `json:"currency"`
	PaymentMethod string    `json:"paymentMethod"`
	Category      string    `json:"category,omitempty"`
	Description   string    `json:"description"`
	Note          string    `json:"note,omitempty"`
	OccurredAt    time.Time `json:"occurredAt"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type CreateManualExpense struct {
	ID             string
	UserID         string
	IdempotencyKey string
	Amount         int64
	Currency       string
	PaymentMethod  string
	Category       string
	Description    string
	Note           string
	OccurredAt     time.Time
}

type UpdateManualExpense struct {
	Amount        *int64
	Currency      *string
	PaymentMethod *string
	Category      *string
	Description   *string
	Note          *string
	OccurredAt    *time.Time
}

func (r *ManualExpenseRepository) Create(ctx context.Context, in CreateManualExpense) (ManualExpense, bool, error) {
	var expense ManualExpense
	err := r.pool.QueryRow(ctx, `
INSERT INTO manual_expenses
  (id,user_id,idempotency_key,amount,currency,payment_method,category,description,note,occurred_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (user_id,idempotency_key) DO NOTHING
RETURNING id,amount,currency,payment_method,category,description,note,occurred_at,created_at,updated_at`,
		in.ID, in.UserID, in.IdempotencyKey, in.Amount, in.Currency, in.PaymentMethod,
		in.Category, in.Description, in.Note, in.OccurredAt,
	).Scan(&expense.ID, &expense.Amount, &expense.Currency, &expense.PaymentMethod, &expense.Category, &expense.Description, &expense.Note, &expense.OccurredAt, &expense.CreatedAt, &expense.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		expense, found, getErr := r.GetByIdempotencyKey(ctx, in.UserID, in.IdempotencyKey)
		if getErr != nil {
			return ManualExpense{}, false, getErr
		}
		if !found {
			return ManualExpense{}, false, ErrManualExpenseIdempotencyConflict
		}
		return expense, true, nil
	}
	if err != nil {
		return ManualExpense{}, false, err
	}
	return presentManualExpense(expense), false, nil
}

func (r *ManualExpenseRepository) List(ctx context.Context, userID string) ([]ManualExpense, error) {
	rows, err := r.pool.Query(ctx, `
SELECT id,amount,currency,payment_method,category,description,note,occurred_at,created_at,updated_at
FROM manual_expenses WHERE user_id=$1 AND deleted_at IS NULL
ORDER BY occurred_at DESC,id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ManualExpense{}
	for rows.Next() {
		var expense ManualExpense
		if err := rows.Scan(&expense.ID, &expense.Amount, &expense.Currency, &expense.PaymentMethod, &expense.Category, &expense.Description, &expense.Note, &expense.OccurredAt, &expense.CreatedAt, &expense.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, presentManualExpense(expense))
	}
	return items, rows.Err()
}

func (r *ManualExpenseRepository) Update(ctx context.Context, userID, id string, in UpdateManualExpense) (ManualExpense, error) {
	var expense ManualExpense
	err := r.pool.QueryRow(ctx, `
UPDATE manual_expenses SET
 amount=COALESCE($3,amount), currency=COALESCE($4,currency), payment_method=COALESCE($5,payment_method),
 category=COALESCE($6,category), description=COALESCE($7,description), note=COALESCE($8,note),
 occurred_at=COALESCE($9,occurred_at), updated_at=NOW()
WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL
RETURNING id,amount,currency,payment_method,category,description,note,occurred_at,created_at,updated_at`,
		id, userID, in.Amount, in.Currency, in.PaymentMethod, in.Category, in.Description, in.Note, in.OccurredAt,
	).Scan(&expense.ID, &expense.Amount, &expense.Currency, &expense.PaymentMethod, &expense.Category, &expense.Description, &expense.Note, &expense.OccurredAt, &expense.CreatedAt, &expense.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManualExpense{}, ErrManualExpenseNotFound
	}
	if err != nil {
		return ManualExpense{}, err
	}
	return presentManualExpense(expense), nil
}

func (r *ManualExpenseRepository) Delete(ctx context.Context, userID, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE manual_expenses SET deleted_at=NOW(),updated_at=NOW() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *ManualExpenseRepository) GetByIdempotencyKey(ctx context.Context, userID, key string) (ManualExpense, bool, error) {
	var expense ManualExpense
	var deletedAt *time.Time
	err := r.pool.QueryRow(ctx, `
SELECT id,amount,currency,payment_method,category,description,note,occurred_at,created_at,updated_at
     ,deleted_at
FROM manual_expenses WHERE user_id=$1 AND idempotency_key=$2`, userID, key).
		Scan(&expense.ID, &expense.Amount, &expense.Currency, &expense.PaymentMethod, &expense.Category, &expense.Description, &expense.Note, &expense.OccurredAt, &expense.CreatedAt, &expense.UpdatedAt, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManualExpense{}, false, nil
	}
	if err != nil {
		return ManualExpense{}, false, err
	}
	if deletedAt != nil {
		return ManualExpense{}, false, ErrManualExpenseIdempotencyConflict
	}
	return presentManualExpense(expense), true, nil
}

func presentManualExpense(expense ManualExpense) ManualExpense {
	expense.Source = "manual"
	expense.Direction = "debit"
	expense.Currency = strings.ToUpper(expense.Currency)
	expense.OccurredAt = expense.OccurredAt.UTC()
	expense.CreatedAt = expense.CreatedAt.UTC()
	expense.UpdatedAt = expense.UpdatedAt.UTC()
	return expense
}
