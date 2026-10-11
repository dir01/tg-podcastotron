-- +migrate Up
-- creation_key identifies the unit of work that created an episode
-- (create request + variant index). Retries of that work find the existing row
-- through it instead of creating a duplicate episode and a duplicate upload.
ALTER TABLE episodes ADD COLUMN creation_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS episodes_user_creation_key
    ON episodes (user_id, creation_key) WHERE creation_key IS NOT NULL;

-- Used to check whether an S3 object is still referenced (delete, gc).
CREATE INDEX IF NOT EXISTS episodes_storage_key ON episodes (storage_key);

-- +migrate Down
DROP INDEX IF EXISTS episodes_storage_key;
DROP INDEX IF EXISTS episodes_user_creation_key;
ALTER TABLE episodes DROP COLUMN creation_key;
