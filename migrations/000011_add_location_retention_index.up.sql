-- Supports the retention pruner's global scan over expired rows.
-- The existing (vehicle_id, received_at DESC) index cannot serve this predicate
-- because received_at is not its leading column.
-- CONCURRENTLY keeps location ingest writable while the index builds. It cannot
-- run inside a transaction, which is fine here: golang-migrate's postgres driver
-- executes migration files directly on the connection, not in a transaction.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_location_points_received_at ON location_points (received_at);
