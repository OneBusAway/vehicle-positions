-- One row for each alert the server raised: when it was raised and, once it
-- cleared, when it cleared. The Alerts page lists the cleared ones.
--
-- trip_id is the trip running, or for a not_started alert the vehicle's last
-- trip; gtfs_trip_id and route_id are the trip not started for that kind, the
-- trip running for the others, as the alert webhook names them.
CREATE TABLE alert_history (
    id           BIGSERIAL   PRIMARY KEY,
    kind         TEXT        NOT NULL,
    vehicle_id   TEXT        NOT NULL,
    trip_id      BIGINT      NOT NULL REFERENCES trips(id) ON DELETE CASCADE,
    gtfs_trip_id TEXT        NOT NULL,
    route_id     TEXT        NOT NULL,
    raised_at    TIMESTAMPTZ NOT NULL,
    cleared_at   TIMESTAMPTZ
);

-- An alert has at most one open row, so an alert still open after a restart
-- keeps the row it was raised under.
CREATE UNIQUE INDEX idx_alert_history_open
    ON alert_history (trip_id, kind, gtfs_trip_id) WHERE cleared_at IS NULL;

-- The Alerts page lists cleared alerts newest first, of every kind or of one.
-- The first also serves the retention delete.
CREATE INDEX idx_alert_history_cleared
    ON alert_history (cleared_at DESC) WHERE cleared_at IS NOT NULL;
CREATE INDEX idx_alert_history_kind_cleared
    ON alert_history (kind, cleared_at DESC) WHERE cleared_at IS NOT NULL;
