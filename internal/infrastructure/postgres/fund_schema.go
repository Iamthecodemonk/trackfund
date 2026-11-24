package postgres

const fundSchema = `
ALTER TABLE fund_summaries ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE fund_summaries ADD COLUMN IF NOT EXISTS target_icon TEXT NOT NULL DEFAULT '';
ALTER TABLE fund_summaries ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE fund_summaries summary
SET user_id = events.event_data->>'OwnerID',
    target_icon = COALESCE(events.event_data->>'TargetIcon','')
FROM event_store events
WHERE events.event_type='fund.created' AND events.stream_id=summary.id AND summary.user_id IS NULL;

CREATE INDEX IF NOT EXISTS fund_summaries_user_created_idx ON fund_summaries(user_id,created_at DESC) WHERE user_id IS NOT NULL;
`
