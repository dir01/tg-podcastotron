-- +migrate Up
-- The cover picture of an episode, from the ID3 tag of its file. The Mini App
-- serves it at /covers/<id>. NULL means that nobody looked for it yet. An empty
-- string means that the file has no cover.
ALTER TABLE episodes ADD COLUMN cover_id TEXT;

CREATE TABLE IF NOT EXISTS covers (
    id   TEXT PRIMARY KEY, -- random, with the file extension, e.g. "1f0c….jpg"
    mime TEXT NOT NULL,
    data BLOB NOT NULL
);

-- +migrate Down
DROP TABLE IF EXISTS covers;
ALTER TABLE episodes DROP COLUMN cover_id;
