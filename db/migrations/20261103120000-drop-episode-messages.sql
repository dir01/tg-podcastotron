-- +migrate Up
-- The bot keeps its status message references in memory now.
DROP TABLE IF EXISTS episode_messages;

-- +migrate Down
CREATE TABLE IF NOT EXISTS episode_messages (
    user_id    TEXT    NOT NULL,
    episode_id TEXT    NOT NULL,
    chat_id    INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    log        TEXT    NOT NULL DEFAULT '[]',
    PRIMARY KEY (user_id, episode_id)
);
