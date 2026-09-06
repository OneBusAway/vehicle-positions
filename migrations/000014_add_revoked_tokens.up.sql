CREATE TABLE IF NOT EXISTS revoked_tokens (
    jti        TEXT PRIMARY KEY CHECK (jti != ''),
    user_id    BIGINT REFERENCES users(id) ON DELETE SET NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Supports the periodic cleanup of expired rows that is planned as a
-- follow-up (DELETE FROM revoked_tokens WHERE expires_at < NOW()). jti needs
-- no index of its own: the primary key already provides one.
CREATE INDEX IF NOT EXISTS idx_revoked_tokens_expires_at ON revoked_tokens (expires_at);
