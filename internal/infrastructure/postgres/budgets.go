package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

var ErrBudgetNotFound = errors.New("budget not found")

type BudgetRepository struct{ pool *pgxpool.Pool }

func NewBudgetRepository(pool *pgxpool.Pool) *BudgetRepository { return &BudgetRepository{pool: pool} }

type BudgetCategory struct {
	CategoryID     string `json:"categoryId"`
	Limit          int64  `json:"limit"`
	AlertThreshold *int   `json:"alertThreshold,omitempty"`
}
type Budget struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Currency    string           `json:"currency"`
	Timezone    string           `json:"timezone"`
	PeriodStart time.Time        `json:"periodStart"`
	PeriodEnd   time.Time        `json:"periodEnd"`
	Categories  []BudgetCategory `json:"categories"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
}
type BudgetInput struct {
	Name, Currency, Timezone string
	PeriodStart, PeriodEnd   time.Time
	Categories               []BudgetCategory
}

func (r *BudgetRepository) Create(ctx context.Context, id, userID string, in BudgetInput) (Budget, error) {
	raw, err := json.Marshal(in.Categories)
	if err != nil {
		return Budget{}, err
	}
	return scanBudget(r.pool.QueryRow(ctx, `INSERT INTO budgets(id,user_id,name,currency,timezone,period_start,period_end,categories) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id,name,currency,timezone,period_start,period_end,categories,created_at,updated_at`, id, userID, in.Name, in.Currency, in.Timezone, in.PeriodStart, in.PeriodEnd, raw))
}
func (r *BudgetRepository) List(ctx context.Context, userID string) ([]Budget, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,name,currency,timezone,period_start,period_end,categories,created_at,updated_at FROM budgets WHERE user_id=$1 AND deleted_at IS NULL ORDER BY period_start DESC,id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Budget{}
	for rows.Next() {
		b, err := scanBudget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (r *BudgetRepository) Get(ctx context.Context, userID, id string) (Budget, error) {
	b, err := scanBudget(r.pool.QueryRow(ctx, `SELECT id,name,currency,timezone,period_start,period_end,categories,created_at,updated_at FROM budgets WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Budget{}, ErrBudgetNotFound
	}
	return b, err
}
func (r *BudgetRepository) Update(ctx context.Context, userID, id string, in BudgetInput) (Budget, error) {
	raw, err := json.Marshal(in.Categories)
	if err != nil {
		return Budget{}, err
	}
	b, err := scanBudget(r.pool.QueryRow(ctx, `UPDATE budgets SET name=$3,currency=$4,timezone=$5,period_start=$6,period_end=$7,categories=$8,updated_at=NOW() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL RETURNING id,name,currency,timezone,period_start,period_end,categories,created_at,updated_at`, id, userID, in.Name, in.Currency, in.Timezone, in.PeriodStart, in.PeriodEnd, raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return Budget{}, ErrBudgetNotFound
	}
	return b, err
}
func (r *BudgetRepository) Delete(ctx context.Context, userID, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE budgets SET deleted_at=NOW(),updated_at=NOW() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID)
	return tag.RowsAffected() == 1, err
}

type scan interface{ Scan(...any) error }

func scanBudget(row scan) (Budget, error) {
	var b Budget
	var raw []byte
	err := row.Scan(&b.ID, &b.Name, &b.Currency, &b.Timezone, &b.PeriodStart, &b.PeriodEnd, &raw, &b.CreatedAt, &b.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &b.Categories)
	}
	if b.Categories == nil {
		b.Categories = []BudgetCategory{}
	}
	return b, err
}
