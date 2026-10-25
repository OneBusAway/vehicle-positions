-- A vehicle runs one trip at a time. Before this index two drivers could hold
-- active trips on the same vehicle, so close all but the newest of any such
-- set first, or the index cannot be built.
UPDATE trips SET status = 'completed', end_time = NOW()
WHERE status = 'active'
  AND id NOT IN (SELECT MAX(id) FROM trips WHERE status = 'active' GROUP BY vehicle_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_trips_one_active_per_vehicle
    ON trips(vehicle_id) WHERE status = 'active';
