-- One row per fix: a phone that retries an upload or report whose reply it
-- never got sends the same fix again, and InsertLocationPoint skips it on this
-- key. Duplicates already stored are removed first, keeping the first received.
-- Not CONCURRENTLY: the delete and the index have to run together, and a
-- concurrent build cannot share a transaction.
DELETE FROM location_points a
USING location_points b
WHERE a.vehicle_id = b.vehicle_id
  AND a.driver_id = b.driver_id
  AND a.timestamp = b.timestamp
  AND a.id > b.id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_location_points_vehicle_driver_timestamp
ON location_points (vehicle_id, driver_id, timestamp);
