-- One row for each hour the server counted report and feed outcomes in,
-- written when the hour closes, so the Feed health page's last day survives a
-- restart. The hour in progress when the server stops is written then too, and
-- a start later in the same hour carries on from that row and writes it again
-- when the hour closes.
--
-- The counts are the ones GET /api/v1/admin/health gives for a minute, over
-- the hour. vehicles is how many vehicles had a live report accepted in the
-- hour, each counted once.
CREATE TABLE feed_health_hours (
    hour                 TIMESTAMPTZ PRIMARY KEY,
    reports_accepted     BIGINT      NOT NULL,
    reports_invalid      BIGINT      NOT NULL,
    reports_unauthorized BIGINT      NOT NULL,
    reports_rate_limited BIGINT      NOT NULL,
    reports_server_error BIGINT      NOT NULL,
    feed_served          BIGINT      NOT NULL,
    feed_refused         BIGINT      NOT NULL,
    feed_invalid         BIGINT      NOT NULL,
    feed_server_error    BIGINT      NOT NULL,
    vehicles             INTEGER     NOT NULL
);
