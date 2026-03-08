CREATE TABLE IF NOT EXISTS vehicles (
    id         TEXT PRIMARY KEY CHECK (id != ''),
    label      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS location_points (
    id          BIGSERIAL PRIMARY KEY,
    vehicle_id  TEXT NOT NULL REFERENCES vehicles(id),
    trip_id     TEXT NOT NULL DEFAULT '',
    latitude    DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude   DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    bearing     DOUBLE PRECISION,
    speed       DOUBLE PRECISION,
    accuracy    DOUBLE PRECISION,
    timestamp   BIGINT NOT NULL CHECK (timestamp > 0),
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_location_points_vehicle_id ON location_points(vehicle_id);
CREATE INDEX IF NOT EXISTS idx_location_points_timestamp ON location_points(timestamp);
