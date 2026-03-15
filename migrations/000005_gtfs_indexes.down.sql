DROP INDEX IF EXISTS idx_location_points_received_at;
DROP INDEX IF EXISTS idx_trips_route_id;
DROP INDEX IF EXISTS idx_stop_times_trip_id;


ALTER TABLE stop_times DROP CONSTRAINT IF EXISTS fk_stop_times_trip_id;
ALTER TABLE trips DROP CONSTRAINT IF EXISTS fk_trips_route_id;
ALTER TABLE stop_times DROP CONSTRAINT IF EXISTS stop_times_trip_id_stop_sequence_key;