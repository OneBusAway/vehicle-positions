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

-- name: ListUsers :many
SELECT id, name, email, role, created_at, updated_at
FROM users
ORDER BY created_at DESC;

-- name: GetUserByID :one
SELECT id, name, email, role, created_at, updated_at
FROM users
WHERE id = $1;

-- name: CreateUser :one
INSERT INTO users (name, email, password_hash, role)
VALUES ($1, $2, $3, $4)
RETURNING id, name, email, role, created_at, updated_at;

-- name: UpdateUser :one
-- updated_at is maintained by the set_users_updated_at trigger.
UPDATE users
SET name = $1, email = $2, role = $3
WHERE id = $4
RETURNING id, name, email, role, created_at, updated_at;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = $1;

-- name: ListVehicles :many
SELECT id, label, agency_tag, active, created_at, updated_at
FROM vehicles
ORDER BY created_at DESC;

-- name: GetVehicleByID :one
SELECT id, label, agency_tag, active, created_at, updated_at
FROM vehicles
WHERE id = $1;

-- name: UpsertAdminVehicle :one
INSERT INTO vehicles (id, label, agency_tag)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET label = EXCLUDED.label, agency_tag = EXCLUDED.agency_tag, active = true, updated_at = NOW()
RETURNING id, label, agency_tag, active, created_at, updated_at;

-- name: DeactivateVehicle :execrows
UPDATE vehicles
SET active = false, updated_at = NOW()
WHERE id = $1;

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

-- name: ListTripsFiltered :many
SELECT id, user_id, vehicle_id, route_id, gtfs_trip_id, start_time, end_time, status, created_at, updated_at
FROM trips
WHERE (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('vehicle_id')::text IS NULL OR vehicle_id = sqlc.narg('vehicle_id'))
  AND (sqlc.narg('user_id')::bigint IS NULL OR user_id = sqlc.narg('user_id'))
ORDER BY start_time DESC
LIMIT sqlc.arg('query_limit') OFFSET sqlc.arg('query_offset');

-- name: GetTripByID :one
SELECT id, user_id, vehicle_id, route_id, gtfs_trip_id, start_time, end_time, status, created_at, updated_at
FROM trips
WHERE id = $1;
