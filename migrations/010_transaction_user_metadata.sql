ALTER TABLE mono_transactions ADD COLUMN IF NOT EXISTS category TEXT;
ALTER TABLE mono_transactions ADD COLUMN IF NOT EXISTS category_id TEXT;
ALTER TABLE mono_transactions ADD COLUMN IF NOT EXISTS user_description TEXT;
ALTER TABLE mono_transactions ADD COLUMN IF NOT EXISTS personal_note TEXT;
ALTER TABLE mono_transactions ADD COLUMN IF NOT EXISTS transfer_group_id TEXT;
ALTER TABLE mono_transactions ADD COLUMN IF NOT EXISTS counts_toward_spending BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE mono_transactions ADD COLUMN IF NOT EXISTS counts_toward_income BOOLEAN NOT NULL DEFAULT TRUE;
