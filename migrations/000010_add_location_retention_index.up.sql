-- Supports the retention pruner's global scan over expired rows.
-- The existing (vehicle_id, received_at DESC) index cannot serve this predicate
-- because received_at is not its leading column.
CREATE INDEX IF NOT EXISTS idx_location_points_received_at ON location_points (received_at);
