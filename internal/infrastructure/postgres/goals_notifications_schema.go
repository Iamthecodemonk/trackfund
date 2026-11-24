package postgres

const goalsNotificationsSchema = `
CREATE TABLE IF NOT EXISTS goals (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 title TEXT NOT NULL, target_amount BIGINT NOT NULL CHECK (target_amount > 0),
 current_amount BIGINT NOT NULL DEFAULT 0 CHECK (current_amount >= 0),
 currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'), target_date TIMESTAMPTZ,
 status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','completed')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS goals_user_idx ON goals(user_id, status, target_date) WHERE deleted_at IS NULL;
CREATE TABLE IF NOT EXISTS notifications (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 dedupe_key TEXT NOT NULL, type TEXT NOT NULL, title TEXT NOT NULL, message TEXT NOT NULL,
 goal_id TEXT REFERENCES goals(id) ON DELETE SET NULL, read_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(user_id,dedupe_key)
);
CREATE INDEX IF NOT EXISTS notifications_user_created_idx ON notifications(user_id, created_at DESC);
ALTER TABLE goals ADD COLUMN IF NOT EXISTS target_icon TEXT NOT NULL DEFAULT '';
`
