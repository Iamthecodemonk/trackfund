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
		`SELECT id, name, target_amount, currency, total_contributions, remaining_amount, contribution_count
		 FROM fund_summaries
		 WHERE id = $1`,
		id,
	).Scan(
		&summary.ID,
		&summary.Name,
		&summary.TargetAmount,
		&summary.Currency,
		&summary.TotalContributions,
		&summary.RemainingAmount,
		&summary.ContributionCount,
	)
	if err == pgx.ErrNoRows {
		return fundsapp.FundSummary{}, false, nil
	}
	if err != nil {
		return fundsapp.FundSummary{}, false, err
	}

	return summary, true, nil
}

func (m *FundSummaryReadModel) Save(summary fundsapp.FundSummary) error {
	_, err := m.pool.Exec(
		context.Background(),
		`INSERT INTO fund_summaries (
			id, name, target_amount, currency, total_contributions, remaining_amount, contribution_count, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			target_amount = EXCLUDED.target_amount,
			currency = EXCLUDED.currency,
			total_contributions = EXCLUDED.total_contributions,
			remaining_amount = EXCLUDED.remaining_amount,
			contribution_count = EXCLUDED.contribution_count,
			updated_at = NOW()`,
		summary.ID,
		summary.Name,
		summary.TargetAmount,
		summary.Currency,
		summary.TotalContributions,
		summary.RemainingAmount,
		summary.ContributionCount,
	)
	return err
}
