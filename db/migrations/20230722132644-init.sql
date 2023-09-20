-- +migrate Up
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS chats (
    user_id TEXT REFERENCES users(id) PRIMARY KEY,
    chat_id INTEGER
);

CREATE TABLE IF NOT EXISTS feeds (
    id TEXT NOT NULL,
    user_id TEXT REFERENCES users(id) NOT NULL,
    title TEXT,
    url TEXT,
    is_permanent BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (id, user_id)
);

CREATE TABLE IF NOT EXISTS episodes (
    id TEXT NOT NULL,
    user_id TEXT REFERENCES users(id) NOT NULL,
    title TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    source_url TEXT,
    source_filepaths TEXT,
    mediary_id TEXT,
    url TEXT,
    status TEXT,
    duration INTEGER,
    file_len_bytes INTEGER,
    format TEXT,
    storage_key TEXT,
    PRIMARY KEY (id, user_id)
);

CREATE TABLE IF NOT EXISTS publications (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    feed_id TEXT NOT NULL,
    episode_id TEXT NOT NULL,
    user_id TEXT REFERENCES users(id) NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS local_ids (
    user_id TEXT REFERENCES users(id) PRIMARY KEY,
    episode_id INTEGER NOT NULL,
    feed_id INTEGER NOT NULL
);


-- +migrate Down
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS chats;
DROP TABLE IF EXISTS feeds;
DROP TABLE IF EXISTS episodes;
DROP TABLE IF EXISTS publications;
DROP TABLE IF EXISTS user_ids;
