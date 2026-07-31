CREATE INDEX IF NOT EXISTS idx_location_points_vehicle_timestamp ON location_points (vehicle_id, timestamp DESC);
