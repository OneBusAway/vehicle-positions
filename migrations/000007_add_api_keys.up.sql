-- Migration to add API keys table and related trigger for updating timestamps
CREATE TABLE IF NOT EXISTS api_keys (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL CHECK (name != ''),
    key_hash     TEXT NOT NULL UNIQUE CHECK (key_hash != ''),
    active       BOOLEAN NOT NULL DEFAULT TRUE,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION set_api_keys_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER set_api_keys_updated_at
BEFORE UPDATE ON api_keys
FOR EACH ROW
EXECUTE FUNCTION set_api_keys_updated_at();
