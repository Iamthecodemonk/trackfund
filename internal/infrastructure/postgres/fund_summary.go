package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	fundsapp "trackfund/internal/application/funds"
)

type FundSummaryReadModel struct {
	pool *pgxpool.Pool
}

func NewFundSummaryReadModel(pool *pgxpool.Pool) *FundSummaryReadModel {
	return &FundSummaryReadModel{pool: pool}
}

func (m *FundSummaryReadModel) GetByID(id string) (fundsapp.FundSummary, bool, error) {
	var summary fundsapp.FundSummary
	err := m.pool.QueryRow(
		context.Background(),
		`SELECT id, COALESCE(user_id, ''), name, target_amount, currency, target_icon, total_contributions, remaining_amount, contribution_count, created_at
		 FROM fund_summaries
		 WHERE id = $1`,
		id,
	).Scan(
		&summary.ID,
		&summary.UserID,
		&summary.Name,
		&summary.TargetAmount,
		&summary.Currency,
		&summary.TargetIcon,
		&summary.TotalContributions,
		&summary.RemainingAmount,
		&summary.ContributionCount,
		&summary.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		return fundsapp.FundSummary{}, false, nil
	}
	if err != nil {
		return fundsapp.FundSummary{}, false, err
	}

	return summary, true, nil
}

func (m *FundSummaryReadModel) ListByOwner(userID string, limit, offset int) ([]fundsapp.FundSummary, int, error) {
	var total int
	if err := m.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM fund_summaries WHERE user_id=$1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.pool.Query(context.Background(), `SELECT id,user_id,name,target_amount,currency,target_icon,total_contributions,remaining_amount,contribution_count,created_at FROM fund_summaries WHERE user_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []fundsapp.FundSummary{}
	for rows.Next() {
		var item fundsapp.FundSummary
		if err := rows.Scan(&item.ID, &item.UserID, &item.Name, &item.TargetAmount, &item.Currency, &item.TargetIcon, &item.TotalContributions, &item.RemainingAmount, &item.ContributionCount, &item.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (m *FundSummaryReadModel) Save(summary fundsapp.FundSummary) error {
	_, err := m.pool.Exec(
		context.Background(),
		`INSERT INTO fund_summaries (
			id, user_id, name, target_amount, currency, target_icon, total_contributions, remaining_amount, contribution_count, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		ON CONFLICT (id) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			name = EXCLUDED.name,
			target_amount = EXCLUDED.target_amount,
			currency = EXCLUDED.currency,
			target_icon = EXCLUDED.target_icon,
			total_contributions = EXCLUDED.total_contributions,
			remaining_amount = EXCLUDED.remaining_amount,
			contribution_count = EXCLUDED.contribution_count,
			updated_at = NOW()`,
		summary.ID, summary.UserID, summary.Name, summary.TargetAmount, summary.Currency, summary.TargetIcon,
		summary.TotalContributions, summary.RemainingAmount, summary.ContributionCount, summary.CreatedAt,
	)
	return err
}
