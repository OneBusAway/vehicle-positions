-- Supports the refresh-token pruner, which deletes WHERE expires_at < cutoff.
-- Without it every pass scans the whole table.
-- Not CONCURRENTLY, unlike 000011. Migrations run at startup, before the server
-- takes requests, and this table holds tens of thousands of rows at most, so a
-- plain build blocks its writes for milliseconds. A failed CONCURRENTLY build
-- would leave an invalid index behind, and IF NOT EXISTS would skip it when the
-- migration is rerun, so the pruner would never get an index that looks present.
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at ON refresh_tokens (expires_at);
