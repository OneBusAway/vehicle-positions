CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         BIGSERIAL PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE CHECK (token_hash != ''),
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Supports deleting every token for one user, which logout does. token_hash
-- needs no index of its own: the UNIQUE constraint already provides one.
-- used_at is nullable so "never used" is distinguishable from "used at the
-- zero time" — rotation depends on that difference.
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens (user_id);
