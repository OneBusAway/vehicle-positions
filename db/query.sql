-- name: CreateVehicle :exec
INSERT INTO vehicles (id, label, agency_id, active)
VALUES (?, ?, ?, ?);

-- name: GetVehicle :one
SELECT * FROM vehicles WHERE id = ?;

-- name: InsertLocation :exec
INSERT INTO locations (
    vehicle_id, trip_id, latitude, longitude, bearing, speed, accuracy, timestamp
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLatestPositionForVehicle :one
SELECT * FROM locations 
WHERE vehicle_id = ? 
ORDER BY timestamp DESC 
LIMIT 1;

-- name: GetActiveVehicles :many
SELECT v.*, l.latitude, l.longitude, l.bearing, l.speed, l.timestamp as location_timestamp, l.trip_id
FROM vehicles v
JOIN locations l ON v.id = l.vehicle_id
WHERE v.active = 1
AND l.id = (
    SELECT id FROM locations 
    WHERE vehicle_id = v.id 
    ORDER BY timestamp DESC 
    LIMIT 1
);
