CREATE TABLE IF NOT EXISTS chat_history (
    user_id text,
    channel_id text,
    timestamp integer,
    message text,
    message_id text,
    guild_id text
);
