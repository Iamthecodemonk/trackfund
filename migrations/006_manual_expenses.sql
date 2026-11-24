CREATE TABLE IF NOT EXISTS manual_expenses (
 id TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 idempotency_key TEXT NOT NULL CHECK (char_length(idempotency_key) BETWEEN 1 AND 200),
 amount BIGINT NOT NULL CHECK (amount > 0),
 currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
 payment_method TEXT NOT NULL CHECK (payment_method IN ('cash','card','transfer','other')),
 category TEXT NOT NULL DEFAULT '' CHECK (char_length(category) <= 80),
 description TEXT NOT NULL CHECK (char_length(description) BETWEEN 1 AND 400),
 note TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 2000),
 occurred_at TIMESTAMPTZ NOT NULL,
 deleted_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE (user_id,idempotency_key)
);

CREATE INDEX IF NOT EXISTS manual_expenses_user_occurred_idx
ON manual_expenses(user_id,occurred_at DESC) WHERE deleted_at IS NULL;
