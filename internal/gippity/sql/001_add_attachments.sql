CREATE TABLE IF NOT EXISTS new_chat_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT,
    channel_id TEXT,
    timestamp INTEGER,
    message TEXT,
    message_id TEXT,
    guild_id TEXT
);

INSERT INTO
    new_chat_history (
        user_id,
        channel_id,
        timestamp,
        message,
        message_id,
        guild_id
    )
SELECT
    user_id,
    channel_id,
    timestamp,
    message,
    message_id,
    guild_id
FROM
    chat_history;

DROP TABLE chat_history;

ALTER TABLE new_chat_history
RENAME TO chat_history;

CREATE TABLE IF NOT EXISTS chat_attachments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    attachment_url TEXT,
    chat_history_message INTEGER,
    FOREIGN KEY (chat_history_message) REFERENCES chat_history (id)
);
