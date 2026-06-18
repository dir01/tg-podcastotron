-- +migrate Up
-- Tracks the single Telegram "status log" message per episode so that status
-- updates edit one message in place instead of sending a new message each time.
CREATE TABLE IF NOT EXISTS episode_messages (
    user_id    TEXT    NOT NULL,
    episode_id TEXT    NOT NULL,
    chat_id    INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    log        TEXT    NOT NULL DEFAULT '[]',
    PRIMARY KEY (user_id, episode_id)
);

-- +migrate Down
DROP TABLE IF EXISTS episode_messages;
