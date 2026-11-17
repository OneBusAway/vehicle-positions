-- A CSV import preview can be confirmed once. The import records the
-- preview's signature in its own transaction, so a second confirm finds it
-- and imports nothing, and a confirm that fails leaves the preview unused.
-- expires_at is when the preview could no longer be confirmed anyway.
CREATE TABLE import_confirmations (
    signature  BYTEA PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL
);
