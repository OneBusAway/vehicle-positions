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
-- safety bound; not pagination. Callers that need one page at a time use
-- ListUsersPage. The id tiebreaker makes the order total, so the bound
-- always truncates the same 1000 rows.
SELECT id, name, email, role, active, created_at, updated_at
FROM users
ORDER BY created_at DESC, id DESC
LIMIT 1000;

-- name: ListUsersPage :many
SELECT id, name, email, role, active, created_at, updated_at
FROM users
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

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
-- safety bound; not pagination. Callers that need one page at a time use
-- ListVehiclesPage. The id tiebreaker makes the order total, so the bound
-- always truncates the same 1000 rows.
SELECT id, label, agency_tag, active, created_at, updated_at
FROM vehicles
ORDER BY created_at DESC, id DESC
LIMIT 1000;

-- name: ListVehiclesPage :many
SELECT id, label, agency_tag, active, created_at, updated_at
FROM vehicles
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: ListActiveVehiclesPage :many
-- The admin vehicle list hides deactivated vehicles unless
-- ?include_inactive=1. Filtering here rather than after the fetch keeps
-- every page a full page.
SELECT id, label, agency_tag, active, created_at, updated_at
FROM vehicles
WHERE active
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

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

-- name: DeleteLocationPointsBefore :execrows
-- Batched retention delete. The ctid subquery bounds each statement so a large
-- backlog is removed over many small transactions instead of one long lock.
DELETE FROM location_points
WHERE ctid IN (
    SELECT expired.ctid FROM location_points AS expired
    WHERE expired.received_at < sqlc.arg('cutoff')
    ORDER BY expired.received_at
    LIMIT sqlc.arg('batch_size')
);

-- name: UpsertRider :one
INSERT INTO riders (id, installation_id, platform, app_id, app_version)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (installation_id) DO UPDATE
  SET last_seen_at = NOW(), app_version = EXCLUDED.app_version, platform = EXCLUDED.platform, app_id = EXCLUDED.app_id
RETURNING *, (xmax = 0) AS created;

-- name: GetRider :one
SELECT * FROM riders WHERE id = $1;

-- name: TouchRider :exec
UPDATE riders SET last_seen_at = NOW() WHERE id = $1;

-- name: ApplyRideOutcome :one
UPDATE riders SET
  score = LEAST(10, GREATEST(-10, score + $2)),
  rides_total = rides_total + 1,
  rides_corroborated = rides_corroborated + $3,
  rides_rejected = rides_rejected + $4,
  last_seen_at = NOW()
WHERE id = $1
RETURNING *;

-- name: SetRiderTier :exec
UPDATE riders SET tier = $2 WHERE id = $1;

-- name: CountRidersByTier :many
SELECT tier, COUNT(*) AS count FROM riders GROUP BY tier;

-- name: InsertRide :one
INSERT INTO rides (id, rider_id, trip_id, start_date, route_id, vehicle_id, boarding_stop_id, destination_stop_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetRide :one
SELECT * FROM rides WHERE id = $1;

-- name: UpdateRideProgress :exec
UPDATE rides SET state = $2, corroborated = $3, points_total = $4, points_matched = $5,
  points_corroborated = $6, points_contradicted = $7, updated_at = NOW()
WHERE id = $1 AND status = 'active';

-- name: EndRide :execrows
UPDATE rides SET status = 'ended', ended_at = NOW(), end_reason = $2, state = $3, corroborated = $4,
  points_total = $5, points_matched = $6, points_corroborated = $7, points_contradicted = $8, updated_at = NOW()
WHERE id = $1 AND status = 'active';

-- name: EndAllActiveRides :execrows
UPDATE rides SET status = 'ended', ended_at = NOW(), end_reason = $1, updated_at = NOW() WHERE status = 'active';

-- name: ListRides :many
SELECT * FROM rides WHERE status = $1 ORDER BY started_at DESC LIMIT $2 OFFSET $3;

-- Points are appended in one pgx batch per request. The EXISTS guard keeps a
-- late batch from appending points to a ride that has already ended, matching
-- the "status = 'active'" guard on UpdateRideProgress.
-- name: InsertRidePoint :batchexec
INSERT INTO ride_points (ride_id, latitude, longitude, accuracy, speed, bearing, timestamp, outcome, corroboration,
  along_shape, distance_to_shape, schedule_deviation_seconds)
SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
WHERE EXISTS (SELECT 1 FROM rides WHERE id = $1 AND status = 'active');

-- name: CountRidePointsForRide :one
SELECT COUNT(*) FROM ride_points WHERE ride_id = $1;

-- name: DeleteRidePointsBefore :execrows
DELETE FROM ride_points WHERE received_at < $1;
