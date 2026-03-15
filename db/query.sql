-- name: UpsertVehicle :exec
INSERT INTO vehicles (id)
VALUES ($1)
ON CONFLICT (id) DO UPDATE SET updated_at = NOW();

-- name: InsertLocationPoint :exec
INSERT INTO location_points (vehicle_id, trip_id, latitude, longitude, bearing, speed, accuracy, timestamp, driver_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetRecentLocations :many
SELECT DISTINCT ON (vehicle_id)
    vehicle_id, trip_id, latitude, longitude, bearing, speed, accuracy, timestamp, driver_id
FROM location_points
WHERE received_at > $1
ORDER BY vehicle_id, received_at DESC;

-- name: CheckUserVehicleAssignment :one
SELECT user_id, vehicle_id
FROM user_vehicles
WHERE user_id = $1 AND vehicle_id = $2;

-- name: GetActiveTripByUser :one
SELECT id, user_id, vehicle_id, route_id, gtfs_trip_id, start_time, end_time, status, created_at, updated_at
FROM trips
WHERE user_id = $1 AND status = 'active';

-- name: StartTrip :one
INSERT INTO trips (user_id, vehicle_id, route_id, gtfs_trip_id)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, vehicle_id, route_id, gtfs_trip_id, start_time, end_time, status, created_at, updated_at;

-- name: EndTrip :execrows
UPDATE trips
SET status = 'completed', end_time = NOW()
WHERE id = $1 AND user_id = $2 AND status = 'active';