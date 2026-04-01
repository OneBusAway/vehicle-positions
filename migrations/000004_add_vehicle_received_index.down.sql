DROP INDEX IF EXISTS idx_location_points_vehicle_received_at;
CREATE INDEX IF NOT EXISTS idx_location_points_vehicle_id ON location_points (vehicle_id);
