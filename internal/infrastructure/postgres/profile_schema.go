package postgres

const profileSchema = `-- Additive profile fields and separate email-change verification state.
ALTER TABLE users ADD COLUMN IF NOT EXISTS photo_url TEXT;
-- Stop and review legacy case-insensitive duplicates instead of merging users.
CREATE UNIQUE INDEX IF NOT EXISTS users_email_ci_unique ON users(lower(email));
CREATE TABLE IF NOT EXISTS profile_email_changes (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 challenge_id TEXT NOT NULL UNIQUE,pending_email TEXT NOT NULL,code_hash TEXT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,attempts INT NOT NULL DEFAULT 0 CHECK(attempts>=0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS profile_rate_limits (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,operation TEXT NOT NULL,
 window_start TIMESTAMPTZ NOT NULL DEFAULT NOW(),request_count INT NOT NULL,
 PRIMARY KEY(user_id,operation)
);
CREATE TABLE IF NOT EXISTS profile_audit_events (
 id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 action TEXT NOT NULL,changed_fields JSONB NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS profile_audit_user_idx ON profile_audit_events(user_id,created_at DESC);

-- Bind new refresh tokens to the account version. NULL preserves pre-migration sessions.
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS token_version INT;
`
