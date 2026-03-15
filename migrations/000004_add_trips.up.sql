CREATE TABLE IF NOT EXISTS user_vehicles (
    user_id    BIGINT NOT NULL REFERENCES users(id),
    vehicle_id TEXT   NOT NULL REFERENCES vehicles(id),
    PRIMARY KEY (user_id, vehicle_id)
);

CREATE TABLE IF NOT EXISTS trips (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT      NOT NULL REFERENCES users(id),
    vehicle_id   TEXT        NOT NULL REFERENCES vehicles(id),
    route_id     TEXT        NOT NULL DEFAULT '',
    gtfs_trip_id TEXT        NOT NULL DEFAULT '',
    start_time   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    end_time     TIMESTAMPTZ,
    status       TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trips_user_status ON trips(user_id, status);
CREATE INDEX IF NOT EXISTS idx_trips_vehicle_status ON trips(vehicle_id, status);
