-- Additive and repeatable. Keep tombstones so delayed webhooks cannot restore deleted links.
ALTER TABLE mono_accounts ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE TABLE IF NOT EXISTS mono_unlink_jobs (
 mono_account_id TEXT PRIMARY KEY REFERENCES mono_accounts(mono_account_id),
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','done','failed')),
 attempts INT NOT NULL DEFAULT 0, next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 last_error_code TEXT, completed_at TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS profile_avatar_objects (
 object_key TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 photo_url TEXT NOT NULL UNIQUE,status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','active','garbage')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),delete_after TIMESTAMPTZ NOT NULL DEFAULT NOW()+INTERVAL '1 hour',
 attempts INT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS profile_avatar_cleanup_idx ON profile_avatar_objects(delete_after) WHERE status<>'active';

-- Preserve names from existing provider snapshots before later partial updates.
UPDATE mono_accounts SET metadata=metadata||jsonb_build_object('bank_name',metadata#>>'{account,institution,name}')
WHERE COALESCE(metadata->>'bank_name','')='' AND COALESCE(metadata#>>'{account,institution,name}','')<>'';
