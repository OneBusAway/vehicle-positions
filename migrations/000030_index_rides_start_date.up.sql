-- Supports CountRidesByDay, which adds up the rides of the Rider mode page's
-- last 28 service days by start_date. No index has start_date first:
-- idx_rides_trip leads with trip_id, and idx_rides_started is started_at,
-- which is not the service day for a ride taken after midnight. Rides are
-- never deleted, so without it every page view would scan the whole table.
-- Not CONCURRENTLY, for the reasons in 000023.
CREATE INDEX IF NOT EXISTS idx_rides_start_date ON rides (start_date);
