-- name: CreateVehicleIfMissing :exec
-- Creates the row of a vehicle reporting for the first time. A vehicle that
-- has one is only read: NOT EXISTS sees the committed row, so no insert is
-- tried, and the report neither rewrites the row nor waits for an admin's
-- uncommitted update of it, as ON CONFLICT DO NOTHING alone would. The
-- ON CONFLICT is for two first reports at once.
INSERT INTO vehicles (id)
SELECT sqlc.arg(id)::text
WHERE NOT EXISTS (SELECT 1 FROM vehicles WHERE id = sqlc.arg(id))
ON CONFLICT (id) DO NOTHING;

-- name: InsertLocationPoint :execrows
-- A fix already stored (the same vehicle, driver and timestamp) is not stored
-- again: a phone retries an upload or report whose reply it never got.
INSERT INTO location_points (vehicle_id, trip_id, route_id, start_date, latitude, longitude, bearing, speed, accuracy, timestamp, driver_id, occupancy_status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (vehicle_id, driver_id, timestamp) DO NOTHING;

-- name: GetRecentLocations :many
-- route_id and start_date are selected because the feed's TripDescriptor is
-- documented to carry what the driver sent: a seeded vehicle that came back
-- without them would publish a narrower descriptor than it did before the
-- restart, and a route-only vehicle would publish none at all. The vehicle's
-- label is joined in for the same reason: it is the feed's vehicle.label, as
-- the latest report's occupancy_status is the feed's occupancy_status. A
-- deactivated vehicle, or one with no active trip, is left out, so a restart
-- cannot put it back in the feed: the tracker's record of a just-ended trip is
-- in memory only,
-- and seeding would otherwise republish its last report for a full staleness
-- threshold. The latest
-- is by the fix's own timestamp: a backlog a phone uploads after a dead zone
-- (POST /api/v1/locations/history) is received after the live reports, and
-- must not pass for the vehicle's latest position.
SELECT DISTINCT ON (lp.vehicle_id)
    lp.vehicle_id, lp.trip_id, lp.route_id, lp.start_date, lp.latitude, lp.longitude,
    lp.bearing, lp.speed, lp.accuracy, lp.timestamp, lp.driver_id, lp.occupancy_status,
    v.label AS vehicle_label
FROM location_points lp
JOIN vehicles v ON v.id = lp.vehicle_id AND v.active
WHERE lp.received_at > $1
  AND lp.vehicle_id IN (SELECT vehicle_id FROM trips WHERE status = 'active')
ORDER BY lp.vehicle_id, lp.timestamp DESC, lp.received_at DESC;

-- name: ListUsers :many
-- safety bound; not pagination. Callers that need one page at a time use
-- ListUsersPage. The id tiebreaker makes the order total, so the bound
-- always truncates the same 1000 rows.
SELECT id, name, email, role, active, created_at, updated_at, last_seen_at
FROM users
ORDER BY created_at DESC, id DESC
LIMIT 1000;

-- name: GetUserByID :one
SELECT id, name, email, role, active, created_at, updated_at, last_seen_at
FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
-- Matches the email in any case, for sign-in and for the CSV import:
-- users_email_lower_key holds lower(email) unique and serves the lookup.
SELECT id, name, email, password_hash, role, active, created_at, updated_at
FROM users
WHERE lower(email) = lower(sqlc.arg(email));

-- name: CreateUser :one
-- Every account is made here, so every stored email is lower-case.
INSERT INTO users (name, email, password_hash, role)
VALUES (sqlc.arg(name), lower(sqlc.arg(email)), sqlc.arg(password_hash), sqlc.arg(role))
RETURNING id, name, email, role, active, created_at, updated_at;

-- name: UpdateUser :one
-- updated_at is maintained by the set_users_updated_at trigger. A role change
-- ends every token issued before it (sessions_valid_after), so giving the old
-- role back later cannot revive them. The role read in the CASE is the row's
-- value before this update. The email is stored lower-case, as CreateUser
-- stores it.
UPDATE users
SET name = sqlc.arg(name), email = lower(sqlc.arg(email)), role = sqlc.arg(role),
    sessions_valid_after = CASE WHEN role <> sqlc.arg(role) THEN NOW() ELSE sessions_valid_after END
WHERE id = sqlc.arg(id)
RETURNING id, name, email, role, active, created_at, updated_at, last_seen_at;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = $1;

-- name: SetUserActive :execrows
-- Deactivation ends every token issued before it (sessions_valid_after), so
-- reactivating the user cannot revive them.
UPDATE users
SET active = $2,
    sessions_valid_after = CASE WHEN $2 THEN sessions_valid_after ELSE NOW() END
WHERE id = $1;

-- name: UpdateUserPassword :execrows
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: UpdateUserLastSeen :exec
-- Stamped on every successful sign-in and token refresh. The
-- set_users_updated_at trigger skips a change to last_seen_at alone, so this
-- does not move updated_at. NOW() is the same all through a transaction, so a
-- second stamp in one would change nothing, and the trigger would then fire
-- and move updated_at: the guard leaves that row out instead.
UPDATE users SET last_seen_at = NOW() WHERE id = $1 AND last_seen_at IS DISTINCT FROM NOW();

-- name: LockUser :one
-- Serializes rotation and revocation of one user's refresh tokens. Both take
-- this before reading or writing refresh_tokens, so a revocation cannot run
-- while a rotation is mid-transaction. The users row is the mutex because the
-- row a revoker has to see, the rotation's replacement, does not exist yet.
-- Login does not take it.
SELECT id FROM users WHERE id = $1 FOR UPDATE;

-- name: LockUserForKeyShare :one
-- StartTrip takes the driver's row before the assignment, the order DeleteUser
-- takes them in (the user, then its cascade into user_vehicles), so a start and
-- a delete queue behind each other instead of deadlocking. KEY SHARE is the
-- lock the trip's foreign key takes on insert anyway.
SELECT id FROM users WHERE id = $1 FOR KEY SHARE;

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

-- name: CheckUserVehicleAssignmentForShare :one
-- FOR SHARE makes an unassign wait until the caller's transaction commits, so
-- the unassign sees a trip the caller starts and refuses, rather than leaving
-- a running trip on a vehicle the driver is no longer assigned to.
SELECT user_id, vehicle_id
FROM user_vehicles
WHERE user_id = $1 AND vehicle_id = $2
FOR SHARE;

-- name: GetLocationAuthorization :one
-- One round trip answers every question the location ingest asks before it
-- accepts a report, and what the feed publishes from it: the vehicle's label
-- and when its trip started. No row means the vehicle does not exist. A
-- driver has at most one active trip (idx_trips_one_active_per_user), so the
-- join adds at most one row.
SELECT
    v.active AS vehicle_active,
    v.label AS vehicle_label,
    EXISTS (
        SELECT 1 FROM user_vehicles uv
        WHERE uv.user_id = sqlc.arg(user_id) AND uv.vehicle_id = v.id
    )::boolean AS assigned,
    (t.id IS NOT NULL)::boolean AS has_active_trip,
    COALESCE(t.route_id, '')::text AS trip_route_id,
    COALESCE(t.gtfs_trip_id, '')::text AS trip_gtfs_trip_id,
    t.start_time AS trip_start_time
FROM vehicles v
LEFT JOIN trips t
    ON t.vehicle_id = v.id AND t.user_id = sqlc.arg(user_id) AND t.status = 'active'
WHERE v.id = sqlc.arg(vehicle_id);

-- name: GetVehicleActiveForShare :one
-- FOR SHARE holds off a concurrent deactivation until the caller's
-- transaction commits, so a trip or assignment never lands on a vehicle that
-- was deactivated while it was being checked.
SELECT active FROM vehicles WHERE id = $1 FOR SHARE;

-- name: GetActiveTripByVehicle :one
SELECT id, user_id FROM trips WHERE vehicle_id = $1 AND status = 'active';

-- name: GetActiveTripByUser :one
SELECT id, user_id, vehicle_id, route_id, gtfs_trip_id, start_time, end_time, status, created_at, updated_at
FROM trips
WHERE user_id = $1 AND status = 'active';

-- name: StartTrip :one
INSERT INTO trips (user_id, vehicle_id, route_id, gtfs_trip_id)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, vehicle_id, route_id, gtfs_trip_id, start_time, end_time, status, created_at, updated_at;

-- name: EndTrip :one
UPDATE trips
SET status = 'completed', end_time = NOW()
WHERE id = $1 AND user_id = $2 AND status = 'active'
RETURNING vehicle_id;

-- name: EndTripByID :execrows
UPDATE trips
SET status = 'completed', end_time = NOW()
WHERE id = $1 AND status = 'active';

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

-- name: ListTrails :many
-- The trails of the trips with these ids, in id order, each in the order
-- driven and capped at 10000 points of its own. The trail of one trip is read
-- with this query too, so the window below is written once.
-- Trail derivation per spec §4.5: location_points.trip_id is a client string,
-- not trips.id, so trail points are matched by vehicle + driver + time window.
-- A live point is in the window when it was recorded after the trip started
-- and either received during the trip or recorded before the second it ended
-- in, so a fix from the trip before, uploaded within five minutes once this
-- trip has begun, lands in that trip and not in this one. A fix stamped with
-- the second the trip ended in may be from after the end, so it counts only if
-- it was received before the end. A backlog point, one
-- received more than five minutes after the phone recorded it
-- (maxTimestampSkew: a live report that late is refused, so only
-- POST /api/v1/locations/history stores one), is placed only by when it was
-- recorded, so a dead zone uploaded later, even during the next trip, lands in
-- the trail where it was driven and in no other. The start is floored
-- because timestamp is whole seconds and a fix from the trip's first second
-- must still count. The upper bound is five minutes past the second the trip
-- ended in, or now for a trip still running: a report dated more than
-- maxTimestampSkew ahead of the server's clock is refused, and received_at is
-- stamped after that check, so a live point received by the end is dated no
-- later unless the database's clock runs behind the server's, and a fix dated
-- later was not driven on this trip either way. Both bounds stand outside the
-- OR, and are bigint like timestamp, so that
-- idx_location_points_vehicle_driver_timestamp reads only the trip's window
-- and not every point the vehicle and driver ever stored.
SELECT t.id, p.latitude, p.longitude, p.bearing, p.speed, p.accuracy,
       p.timestamp, p.trip_id, p.received_at
FROM trips t
CROSS JOIN LATERAL (
    SELECT lp.latitude, lp.longitude, lp.bearing, lp.speed, lp.accuracy,
           lp.timestamp, lp.trip_id, lp.received_at
    FROM location_points lp
    WHERE lp.vehicle_id = t.vehicle_id
      AND lp.driver_id = t.user_id::text
      AND lp.timestamp >= FLOOR(EXTRACT(EPOCH FROM t.start_time))::bigint
      AND lp.timestamp <= FLOOR(EXTRACT(EPOCH FROM COALESCE(t.end_time, NOW())))::bigint + 300
      AND (
        (lp.received_at >= t.start_time
            AND (lp.received_at <= COALESCE(t.end_time, NOW())
                OR lp.timestamp < FLOOR(EXTRACT(EPOCH FROM t.end_time)))
            AND lp.received_at <= to_timestamp(lp.timestamp) + interval '5 minutes')
        OR (lp.received_at > to_timestamp(lp.timestamp) + interval '5 minutes'
            AND lp.timestamp <= EXTRACT(EPOCH FROM COALESCE(t.end_time, NOW())))
      )
    ORDER BY lp.timestamp ASC, lp.received_at ASC
    LIMIT 10000
) p
WHERE t.id = ANY(sqlc.arg('trip_ids')::bigint[])
ORDER BY t.id, p.timestamp, p.received_at;

-- name: ListActiveTripsByVehicle :many
-- idx_trips_one_active_per_vehicle allows one active trip per vehicle; the
-- DISTINCT ON keeps the answer to one row per vehicle regardless.
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
RETURNING sqlc.embed(riders), (xmax = 0) AS created;

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

-- Returns the affected-row count so a batch that lands after the ride ended
-- rolls back rather than committing points the InsertRidePoint guard already
-- dropped, leaving the counters describing a ride nobody can add to.
-- name: UpdateRideProgress :execrows
UPDATE rides SET state = $2, corroborated = $3, points_total = $4, points_matched = $5,
  points_corroborated = $6, points_contradicted = $7, updated_at = NOW()
WHERE id = $1 AND status = 'active';

-- Returning the rider is what makes ending a ride one round trip: no row came
-- back means the ride was not active, and the id is the one whose reputation
-- the outcome is applied to.
-- name: EndRide :one
UPDATE rides SET status = 'ended', ended_at = NOW(), end_reason = $2, state = $3, corroborated = $4,
  points_total = $5, points_matched = $6, points_corroborated = $7, points_contradicted = $8, updated_at = NOW()
WHERE id = $1 AND status = 'active'
RETURNING rider_id;

-- name: EndAllActiveRides :execrows
UPDATE rides SET status = 'ended', ended_at = NOW(), end_reason = $1, updated_at = NOW() WHERE status = 'active';

-- started_at alone is not a total order, so two rides sharing one can swap
-- between pages and be listed twice or not at all; id breaks the tie.
-- name: ListRides :many
SELECT * FROM rides WHERE status = $1 ORDER BY started_at DESC, id DESC LIMIT $2 OFFSET $3;

-- name: ListRidesOfAnyStatus :many
SELECT * FROM rides ORDER BY started_at DESC, id DESC LIMIT $1 OFFSET $2;

-- A day's trips are its distinct trip_ids: two riders on one trip cover it
-- once. Ranged on start_date, so idx_rides_start_date reads only the period.
-- name: CountRidesByServiceDay :many
SELECT start_date,
       COUNT(*)::int AS rides,
       COUNT(DISTINCT trip_id)::int AS trips,
       SUM(points_matched)::bigint AS points_matched,
       SUM(points_total)::bigint AS points_total
FROM rides
WHERE start_date BETWEEN sqlc.arg('first_date') AND sqlc.arg('last_date')
GROUP BY start_date
ORDER BY start_date;

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

-- name: GetAPIKeyByHash :one
-- Inactive keys are returned too: the middleware distinguishes a revoked key
-- from an unknown one.
SELECT id, name, key_hash, active, last_used_at, created_at, updated_at
FROM api_keys
WHERE key_hash = $1;

-- name: UpdateAPIKeyLastUsed :exec
UPDATE api_keys
SET last_used_at = NOW()
WHERE id = $1;

-- name: CreateAPIKey :one
INSERT INTO api_keys (name, key_hash)
VALUES ($1, $2)
RETURNING id, name, key_hash, active, last_used_at, created_at, updated_at;

-- name: ListAPIKeysPage :many
-- One page of keys, newest first, for the admin page and the admin API. The id
-- tiebreaker makes the order total, so a key created in the same instant as
-- another cannot be on two pages or none.
SELECT id, name, key_hash, active, last_used_at, created_at, updated_at
FROM api_keys
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: DeactivateAPIKey :execrows
UPDATE api_keys
SET active = false, updated_at = NOW()
WHERE id = $1;

-- name: CountActiveAPIKeys :one
SELECT COUNT(*) FROM api_keys WHERE active = true;

-- name: RevokeToken :exec
-- Idempotent: logging out twice must not error.
INSERT INTO revoked_tokens (jti, user_id, expires_at)
VALUES ($1, $2, $3)
ON CONFLICT (jti) DO NOTHING;

-- name: IsTokenRevoked :one
SELECT EXISTS(SELECT 1 FROM revoked_tokens WHERE jti = $1);

-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (token_hash, user_id, expires_at)
VALUES ($1, $2, $3);

-- name: GetRefreshTokenByHash :one
SELECT id, token_hash, user_id, expires_at, used_at, created_at
FROM refresh_tokens
WHERE token_hash = $1;

-- name: MarkRefreshTokenUsed :execrows
-- The used_at IS NULL guard makes consumption a compare-and-set: two
-- concurrent refreshes with the same token both read an unused row, but only
-- one updates it. Zero rows affected means this caller lost that race.
UPDATE refresh_tokens
SET used_at = NOW()
WHERE id = $1 AND used_at IS NULL;

-- name: DeleteRefreshTokensForUser :exec
DELETE FROM refresh_tokens WHERE user_id = $1;

-- name: DeleteExpiredRefreshTokens :execrows
-- Garbage collection, not retention policy: the refresh handler rejects a
-- token past expires_at whether or not its row exists, so deleting it cannot
-- make any token usable or unusable. Used rows need no rule of their own: a
-- consumed row is worth keeping only so a replay reads as reuse rather than
-- as unknown, and this predicate already keeps it until expires_at.
DELETE FROM refresh_tokens WHERE expires_at < sqlc.arg('cutoff');

-- name: GetGTFSFeedCache :one
SELECT source, body, fetched_at FROM gtfs_feed_cache WHERE id = 1;

-- name: GetGTFSFeedCacheDigest :one
SELECT source, sha256 FROM gtfs_feed_cache WHERE id = 1;

-- name: UpsertGTFSFeedCache :exec
INSERT INTO gtfs_feed_cache (id, source, body, sha256, fetched_at)
VALUES (1, $1, $2, $3, NOW())
ON CONFLICT (id) DO UPDATE
SET source = EXCLUDED.source, body = EXCLUDED.body, sha256 = EXCLUDED.sha256, fetched_at = NOW();

-- name: IsUserActiveInRole :one
-- A token's role claim is fixed when it is issued, so a user whose role has
-- changed since no longer holds the role the token claims. A token issued
-- before sessions_valid_after was ended by a deactivation or a role change and
-- stays ended, whatever the user's active flag and role are now.
SELECT EXISTS (
    SELECT 1 FROM users
    WHERE id = sqlc.arg(id) AND active AND role = sqlc.arg(role)
      AND (sessions_valid_after IS NULL OR sessions_valid_after < sqlc.arg(issued_at))
);

-- name: RecordImportConfirmation :execrows
-- No row is inserted for a signature already recorded: that preview was
-- imported.
INSERT INTO import_confirmations (signature, expires_at)
VALUES (sqlc.arg(signature), sqlc.arg(expires_at))
ON CONFLICT (signature) DO NOTHING;

-- name: DeleteImportConfirmationsBefore :exec
DELETE FROM import_confirmations WHERE expires_at < sqlc.arg(cutoff);

-- name: RaiseAlert :one
-- An alert already open, one the server was watching when it restarted,
-- keeps its row and the time it was raised. The update changes nothing: it is
-- there so RETURNING gives that row's id.
INSERT INTO alert_history (kind, vehicle_id, trip_id, gtfs_trip_id, route_id, raised_at)
VALUES (sqlc.arg(kind), sqlc.arg(vehicle_id), sqlc.arg(trip_id), sqlc.arg(gtfs_trip_id), sqlc.arg(route_id), sqlc.arg(raised_at))
ON CONFLICT (trip_id, kind, gtfs_trip_id) WHERE cleared_at IS NULL
DO UPDATE SET raised_at = alert_history.raised_at
RETURNING id;

-- name: ClearAlert :exec
UPDATE alert_history SET cleared_at = sqlc.arg(cleared_at)
WHERE id = sqlc.arg(id) AND cleared_at IS NULL;

-- name: ClearAlertsLeftOpen :execrows
-- Clears every open alert but the ones in keep: after a restart, the alerts
-- the server was watching that have not come back.
UPDATE alert_history SET cleared_at = sqlc.arg(cleared_at)
WHERE cleared_at IS NULL AND NOT (id = ANY(sqlc.arg(keep)::BIGINT[]));

-- name: ListClearedAlerts :many
SELECT a.id, a.kind, a.vehicle_id, COALESCE(v.label, '')::TEXT AS vehicle_label, a.trip_id,
       a.gtfs_trip_id, a.route_id, a.raised_at, a.cleared_at
FROM alert_history a
LEFT JOIN vehicles v ON v.id = a.vehicle_id
WHERE a.cleared_at IS NOT NULL
ORDER BY a.cleared_at DESC, a.id DESC
LIMIT $1 OFFSET $2;

-- name: ListClearedAlertsOfKind :many
SELECT a.id, a.kind, a.vehicle_id, COALESCE(v.label, '')::TEXT AS vehicle_label, a.trip_id,
       a.gtfs_trip_id, a.route_id, a.raised_at, a.cleared_at
FROM alert_history a
LEFT JOIN vehicles v ON v.id = a.vehicle_id
WHERE a.cleared_at IS NOT NULL AND a.kind = $1
ORDER BY a.cleared_at DESC, a.id DESC
LIMIT $2 OFFSET $3;

-- name: DeleteClearedAlertsBefore :execrows
-- An open alert is kept, however old: the server is still watching it.
DELETE FROM alert_history WHERE cleared_at < sqlc.arg(cutoff);

-- name: ListServiceDayTotals :many
SELECT service_date, on_time_early_s, on_time_late_s, totals
FROM service_day_totals
WHERE service_date BETWEEN sqlc.arg(first_date) AND sqlc.arg(last_date)
ORDER BY service_date;

-- name: SaveServiceDayTotals :exec
-- A day worked out again, under another on-time window, replaces its row.
INSERT INTO service_day_totals (service_date, on_time_early_s, on_time_late_s, totals)
VALUES (sqlc.arg(service_date), sqlc.arg(on_time_early_s), sqlc.arg(on_time_late_s), sqlc.arg(totals))
ON CONFLICT (service_date) DO UPDATE
SET on_time_early_s = EXCLUDED.on_time_early_s, on_time_late_s = EXCLUDED.on_time_late_s,
    totals = EXCLUDED.totals, written_at = now();

-- name: DeleteServiceDayTotalsBefore :execrows
DELETE FROM service_day_totals WHERE service_date < sqlc.arg(cutoff);

-- name: ListFeedHealthHours :many
SELECT hour, reports_accepted, reports_invalid, reports_unauthorized, reports_rate_limited,
       reports_server_error, feed_served, feed_refused, feed_invalid, feed_server_error, vehicles
FROM feed_health_hours
WHERE hour BETWEEN sqlc.arg(first_hour) AND sqlc.arg(last_hour)
ORDER BY hour;

-- name: SaveFeedHealthHour :exec
-- An hour saved again replaces its row: a server started in an hour already
-- saved carries on from the row's counts, so its own include them.
INSERT INTO feed_health_hours (hour, reports_accepted, reports_invalid, reports_unauthorized,
    reports_rate_limited, reports_server_error, feed_served, feed_refused, feed_invalid,
    feed_server_error, vehicles)
VALUES (sqlc.arg(hour), sqlc.arg(reports_accepted), sqlc.arg(reports_invalid), sqlc.arg(reports_unauthorized),
    sqlc.arg(reports_rate_limited), sqlc.arg(reports_server_error), sqlc.arg(feed_served), sqlc.arg(feed_refused),
    sqlc.arg(feed_invalid), sqlc.arg(feed_server_error), sqlc.arg(vehicles))
ON CONFLICT (hour) DO UPDATE
SET reports_accepted = EXCLUDED.reports_accepted, reports_invalid = EXCLUDED.reports_invalid,
    reports_unauthorized = EXCLUDED.reports_unauthorized, reports_rate_limited = EXCLUDED.reports_rate_limited,
    reports_server_error = EXCLUDED.reports_server_error, feed_served = EXCLUDED.feed_served,
    feed_refused = EXCLUDED.feed_refused, feed_invalid = EXCLUDED.feed_invalid,
    feed_server_error = EXCLUDED.feed_server_error, vehicles = EXCLUDED.vehicles;

-- name: DeleteFeedHealthHoursBefore :execrows
DELETE FROM feed_health_hours WHERE hour < sqlc.arg(cutoff);

-- name: GetVehicleForUpdate :one
-- Locks the vehicle for the rest of the transaction, so the fields read here
-- are the ones the change that follows replaces. NO KEY UPDATE holds off any
-- other change to the row as FOR UPDATE would, but lets through the KEY SHARE
-- lock a foreign key check takes, so inserting a row that points at the
-- vehicle does not wait for the admin's change to commit. None of the changes
-- that follow rewrites the id.
SELECT label, agency_tag, active FROM vehicles WHERE id = $1 FOR NO KEY UPDATE;

-- name: GetUserForUpdate :one
-- Locks the user for the rest of the transaction, against LockUser too, and
-- reads the fields the change that follows replaces. NO KEY UPDATE for the
-- reason GetVehicleForUpdate gives: a trip or assignment for the user does not
-- wait. DeleteUser's DELETE takes the full lock when it runs.
SELECT name, email, role, active FROM users WHERE id = $1 FOR NO KEY UPDATE;

-- name: GetAPIKeyForUpdate :one
SELECT name, active FROM api_keys WHERE id = $1 FOR NO KEY UPDATE;

-- name: InsertAuditEntry :exec
INSERT INTO audit_log (actor_id, actor_email, actor_ip, object_type, object_id, action, before, after)
VALUES (sqlc.narg(actor_id), sqlc.arg(actor_email), sqlc.arg(actor_ip), sqlc.arg(object_type),
    sqlc.arg(object_id), sqlc.arg(action), sqlc.narg(before), sqlc.narg(after));

-- name: ListAuditEntries :many
-- One page of the audit log, newest first, narrowed to one kind of object, one
-- object of that kind, or the changes one admin made, by whichever are given.
-- account_name is the name the account a row is about has now: '' for a row
-- about something else, or about an account since deleted.
SELECT a.id, a.at, a.actor_id, a.actor_email, a.actor_ip, a.object_type, a.object_id, a.action,
       a.before, a.after, COALESCE(u.name, '')::TEXT AS account_name
FROM audit_log a
LEFT JOIN users u ON a.object_type = 'user' AND u.id::TEXT = a.object_id
WHERE (sqlc.narg(object_type)::TEXT IS NULL OR a.object_type = sqlc.narg(object_type))
  AND (sqlc.narg(object_id)::TEXT IS NULL OR a.object_id = sqlc.narg(object_id))
  AND (sqlc.narg(actor_id)::BIGINT IS NULL OR a.actor_id = sqlc.narg(actor_id))
ORDER BY a.id DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);
