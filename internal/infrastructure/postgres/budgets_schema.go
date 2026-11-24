package postgres

const budgetsSchema = `
CREATE TABLE IF NOT EXISTS budgets (
 id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,name TEXT NOT NULL,
 currency TEXT NOT NULL CHECK(currency ~ '^[A-Z]{3}$'),timezone TEXT NOT NULL,period_start TIMESTAMPTZ NOT NULL,period_end TIMESTAMPTZ NOT NULL,
 categories JSONB NOT NULL DEFAULT '[]'::jsonb,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),deleted_at TIMESTAMPTZ,
 CHECK(period_end > period_start)
);CREATE INDEX IF NOT EXISTS budgets_user_period_idx ON budgets(user_id,period_start DESC) WHERE deleted_at IS NULL;`
