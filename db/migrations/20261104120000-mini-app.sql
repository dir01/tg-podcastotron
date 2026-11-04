-- +migrate Up
-- The status history of each episode. The Mini App shows it as a timeline.
CREATE TABLE IF NOT EXISTS episode_events (
    id           INTEGER  PRIMARY KEY AUTOINCREMENT,
    user_id      TEXT     NOT NULL,
    episode_id   TEXT     NOT NULL,
    at           DATETIME NOT NULL,
    status       TEXT     NOT NULL,
    attempt      INTEGER  NOT NULL DEFAULT 0,
    max_attempts INTEGER  NOT NULL DEFAULT 0,
    detail       TEXT     NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS episode_events_episode ON episode_events (user_id, episode_id, id);

-- A link that a user sent to the bot or pasted in the Mini App. The bot gives
-- a button with the short ID, because a magnet link can be too long for a button URL.
CREATE TABLE IF NOT EXISTS source_links (
    id         TEXT     PRIMARY KEY,
    user_id    TEXT     NOT NULL,
    url        TEXT     NOT NULL,
    created_at DATETIME NOT NULL
);

-- One-time invite links that the admin makes in the Mini App.
CREATE TABLE IF NOT EXISTS invites (
    token      TEXT     PRIMARY KEY,
    created_by TEXT     NOT NULL,
    created_at DATETIME NOT NULL,
    used_by    TEXT,
    used_at    DATETIME
);

-- +migrate Down
DROP TABLE IF EXISTS invites;
DROP TABLE IF EXISTS source_links;
DROP INDEX IF EXISTS episode_events_episode;
DROP TABLE IF EXISTS episode_events;
