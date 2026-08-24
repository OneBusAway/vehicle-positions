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
SELECT id, name, email, role, active, created_at, updated_at
FROM users
ORDER BY created_at DESC;

-- name: GetUserByID :one
SELECT id, name, email, role, active, created_at, updated_at
FROM users
WHERE id = $1;

-- name: CreateUser :one
INSERT INTO users (name, email, password_hash, role)
VALUES ($1, $2, $3, $4)
RETURNING id, name, email, role, active, created_at, updated_at;

-- name: UpdateUser :one
-- updated_at is maintained by the set_users_updated_at trigger.
UPDATE users
SET name = $1, email = $2, role = $3
WHERE id = $4
RETURNING id, name, email, role, active, created_at, updated_at;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = $1;

-- name: SetUserActive :execrows
UPDATE users SET active = $2 WHERE id = $1;

-- name: UpdateUserPassword :execrows
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: CountUsersByRole :one
SELECT COUNT(*) FROM users WHERE role = $1;

-- name: CountActiveUsersByRole :one
SELECT COUNT(*) FROM users WHERE role = $1 AND active = true;

-- name: ListVehicles :many
SELECT id, label, agency_tag, active, created_at, updated_at
FROM vehicles
ORDER BY created_at DESC;

-- name: GetVehicleByID :one
SELECT id, label, agency_tag, active, created_at, updated_at
FROM vehicles
WHERE id = $1;

-- name: CreateVehicle :execrows
INSERT INTO vehicles (id, label, agency_tag)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO NOTHING;

-- name: UpsertAdminVehicle :one
INSERT INTO vehicles (id, label, agency_tag)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET label = EXCLUDED.label, agency_tag = EXCLUDED.agency_tag, active = true, updated_at = NOW()
RETURNING id, label, agency_tag, active, created_at, updated_at;

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

-- name: AssignUserVehicle :one
INSERT INTO user_vehicles (user_id, vehicle_id)
VALUES ($1, $2)
RETURNING user_id, vehicle_id, created_at;

-- name: UnassignUserVehicle :execrows
DELETE FROM user_vehicles
WHERE user_id = $1 AND vehicle_id = $2;

-- name: ListVehiclesByUser :many
-- safety bound; not pagination
SELECT user_id, vehicle_id, created_at
FROM user_vehicles
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT 1000;

-- name: ListUsersByVehicle :many
-- safety bound; not pagination
SELECT user_id, vehicle_id, created_at
FROM user_vehicles
WHERE vehicle_id = $1
ORDER BY created_at DESC
LIMIT 1000;

-- name: ListActiveVehiclesByUser :many
-- Driver-facing: active vehicles assigned to a user. LIMIT is a safety bound, not pagination.
SELECT v.id, v.label, v.agency_tag, v.active, v.created_at, v.updated_at
FROM vehicles v
JOIN user_vehicles uv ON uv.vehicle_id = v.id
WHERE uv.user_id = $1 AND v.active = TRUE
ORDER BY v.label, v.id
LIMIT 1000;

-- name: GetLocationHistory :many
SELECT latitude, longitude, bearing, speed, accuracy, timestamp, trip_id, received_at
FROM location_points
WHERE vehicle_id = $1
  AND timestamp >= $2
  AND timestamp <= $3
ORDER BY timestamp DESC
LIMIT $4;

-- name: VehicleExists :one
SELECT EXISTS(SELECT 1 FROM vehicles WHERE id = $1);

-- name: UpdateVehicleInfo :execrows
UPDATE vehicles SET label = $2, agency_tag = $3, updated_at = NOW() WHERE id = $1;

-- name: SetVehicleActive :execrows
UPDATE vehicles SET active = $2, updated_at = NOW() WHERE id = $1;

-- name: CountActiveVehicles :one
SELECT COUNT(*) FROM vehicles WHERE active = true;

-- name: CountActiveTrips :one
SELECT COUNT(*) FROM trips WHERE status = 'active';

-- name: GetTripSummary :one
SELECT t.id, t.vehicle_id, v.label AS vehicle_label, t.user_id, u.name AS driver_name,
       t.route_id, t.gtfs_trip_id, t.start_time, t.end_time, t.status
FROM trips t
JOIN users u ON u.id = t.user_id
JOIN vehicles v ON v.id = t.vehicle_id
WHERE t.id = $1;

-- name: ListTripLocations :many
-- Trail derivation per spec §4.5: location_points.trip_id is a client string,
-- not trips.id, so trail points are matched by vehicle + driver + time window.
SELECT lp.latitude, lp.longitude, lp.bearing, lp.speed, lp.accuracy,
       lp.timestamp, lp.trip_id, lp.received_at
FROM location_points lp
JOIN trips t ON t.id = $1
WHERE lp.vehicle_id = t.vehicle_id
  AND lp.driver_id = t.user_id::text
  AND lp.received_at >= t.start_time
  AND lp.received_at <= COALESCE(t.end_time, NOW())
ORDER BY lp.received_at ASC
LIMIT 10000;

-- name: ListActiveTripsByVehicle :many
-- Schema guarantees one active trip per USER, not per vehicle; newest active
-- trip per vehicle is the defined tiebreak (spec §4.8).
SELECT DISTINCT ON (t.vehicle_id)
       t.vehicle_id, t.id, t.route_id, t.gtfs_trip_id, t.user_id, u.name AS driver_name
FROM trips t
JOIN users u ON u.id = t.user_id
WHERE t.status = 'active'
ORDER BY t.vehicle_id, t.start_time DESC;
