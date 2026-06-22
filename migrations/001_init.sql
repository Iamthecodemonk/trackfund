CREATE TABLE IF NOT EXISTS event_store (
	id BIGSERIAL PRIMARY KEY,
	event_id TEXT NOT NULL UNIQUE,
	stream_id TEXT NOT NULL,
	version INT NOT NULL,
	event_type TEXT NOT NULL,
	event_data JSONB NOT NULL,
	occurred_at TIMESTAMPTZ NOT NULL,
	UNIQUE (stream_id, version)
);

CREATE INDEX IF NOT EXISTS event_store_stream_id_idx ON event_store (stream_id);

CREATE TABLE IF NOT EXISTS fund_summaries (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	target_amount BIGINT NOT NULL,
	currency TEXT NOT NULL,
	total_contributions BIGINT NOT NULL,
	remaining_amount BIGINT NOT NULL,
	contribution_count INT NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	full_name TEXT NOT NULL,
	email TEXT NOT NULL UNIQUE,
	phone TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL CHECK (role IN ('USER', 'ADMIN', 'SUPER_ADMIN')),
	status TEXT NOT NULL CHECK (status IN ('pending', 'active', 'suspended', 'deleted')),
	is_email_verified BOOLEAN NOT NULL DEFAULT FALSE,
	token_version INT NOT NULL DEFAULT 0,
	last_login_at TIMESTAMPTZ,
	password_changed_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS users_email_idx ON users (email);

CREATE TABLE IF NOT EXISTS refresh_tokens (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	token_hash TEXT NOT NULL UNIQUE,
	device_name TEXT NOT NULL DEFAULT '',
	ip_address TEXT NOT NULL DEFAULT '',
	user_agent TEXT NOT NULL DEFAULT '',
	expires_at TIMESTAMPTZ NOT NULL,
	revoked_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS refresh_tokens_user_id_idx ON refresh_tokens (user_id);
CREATE INDEX IF NOT EXISTS refresh_tokens_token_hash_idx ON refresh_tokens (token_hash);

CREATE TABLE IF NOT EXISTS otps (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	email TEXT NOT NULL,
	otp_hash TEXT NOT NULL,
	type TEXT NOT NULL CHECK (type IN ('email_verification', 'password_reset')),
	expires_at TIMESTAMPTZ NOT NULL,
	attempts INT NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS otps_email_type_idx ON otps (email, type, created_at DESC);

CREATE TABLE IF NOT EXISTS pending_users (
	id TEXT PRIMARY KEY,
	full_name TEXT NOT NULL,
	email TEXT NOT NULL UNIQUE,
	phone TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL,
	status TEXT NOT NULL,
	is_email_verified BOOLEAN NOT NULL DEFAULT FALSE,
	token_version INT NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS oauth_providers (
	id TEXT PRIMARY KEY,
	provider TEXT NOT NULL,
	provider_id TEXT NOT NULL,
	user_id TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE(provider, provider_id)
);

-- Mono integration tables: store linked accounts and transactions
CREATE TABLE IF NOT EXISTS mono_accounts (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	mono_account_id TEXT NOT NULL UNIQUE,
	provider TEXT NOT NULL,
	account_name TEXT NOT NULL,
	account_number TEXT,
	bank_code TEXT,
	currency TEXT,
	metadata JSONB,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mono_transactions (
	id TEXT PRIMARY KEY,
	mono_account_id TEXT NOT NULL REFERENCES mono_accounts(id) ON DELETE CASCADE,
	transaction_id TEXT NOT NULL UNIQUE,
	amount BIGINT NOT NULL,
	currency TEXT NOT NULL,
	paid_at TIMESTAMPTZ NOT NULL,
	description TEXT,
	raw JSONB,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mono_transactions_account_idx ON mono_transactions (mono_account_id);

