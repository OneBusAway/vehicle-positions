-- name: UpsertVehicle :exec
INSERT INTO vehicles (id)
VALUES ($1)
ON CONFLICT (id) DO UPDATE SET updated_at = NOW();

-- name: InsertLocationPoint :exec
INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, bearing, speed, accuracy, timestamp)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetRecentLocations :many
SELECT DISTINCT ON (vehicle_id)
    vehicle_id, trip_id, latitude, longitude, bearing, speed, accuracy, timestamp
FROM location_points
WHERE received_at > $1
ORDER BY vehicle_id, received_at DESC;
