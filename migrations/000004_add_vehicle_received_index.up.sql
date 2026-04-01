-- This index allows the database to quickly find the most recent location for each vehicle
CREATE INDEX IF NOT EXISTS idx_location_points_vehicle_received_at
ON location_points (vehicle_id, received_at DESC);

-- Dropping the index on vehicle_id alone, as the new index covers queries that filter by vehicle_id and order by received_at
DROP INDEX IF EXISTS idx_location_points_vehicle_id;
