package postgres

const monoWebhookSchema = `-- Additive, repeatable storage for durable Mono webhook processing.
CREATE TABLE IF NOT EXISTS mono_accounts (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 mono_account_id TEXT NOT NULL UNIQUE, provider TEXT NOT NULL, account_name TEXT NOT NULL,
 account_number TEXT, bank_code TEXT, currency TEXT, metadata JSONB,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS mono_transactions (
 id TEXT PRIMARY KEY,mono_account_id TEXT NOT NULL REFERENCES mono_accounts(id) ON DELETE CASCADE,
 transaction_id TEXT NOT NULL UNIQUE,amount BIGINT NOT NULL,currency TEXT NOT NULL,
 paid_at TIMESTAMPTZ NOT NULL,description TEXT,raw JSONB,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS mono_transactions_account_idx ON mono_transactions(mono_account_id);
ALTER TABLE mono_accounts ADD COLUMN IF NOT EXISTS last_event_at TIMESTAMPTZ;
ALTER TABLE mono_accounts ADD COLUMN IF NOT EXISTS last_synced_at TIMESTAMPTZ;
ALTER TABLE mono_link_sessions ADD COLUMN IF NOT EXISTS mono_account_id TEXT REFERENCES mono_accounts(mono_account_id);
CREATE TABLE IF NOT EXISTS mono_webhook_events (
 event_id TEXT PRIMARY KEY,event_type TEXT NOT NULL,payload JSONB NOT NULL,payload_hash TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processed','failed')),
 attempts INT NOT NULL DEFAULT 0,next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 last_error_code TEXT,received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),processed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS mono_webhook_pending_idx ON mono_webhook_events(next_attempt_at,received_at) WHERE status='pending';
`
