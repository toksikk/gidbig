-- Add is_bot_mention flag to existing rows (default 0 = not a bot mention)
ALTER TABLE chat_history ADD COLUMN is_bot_mention INTEGER DEFAULT 0;

-- Per-user privacy preference: privacy_enabled=1 (on) by default
CREATE TABLE IF NOT EXISTS user_privacy (
    user_id TEXT PRIMARY KEY,
    privacy_enabled INTEGER NOT NULL DEFAULT 1
);
