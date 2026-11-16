-- Supports ListTripsStartedBetween, which reads the trips started in a window:
-- the Reports page's day, and the last day for the not-started alert, which
-- the sidebar's Alerts badge works out on every admin page. Without it every
-- call scans the whole table, and trips only grows: nothing deletes them.
-- Not CONCURRENTLY, as in 000018. Migrations run at startup, before the server
-- takes requests, and a plain build of a year's trips takes under a second.
-- A failed CONCURRENTLY build would leave an invalid index behind, and IF NOT
-- EXISTS would skip it when the migration is rerun.
CREATE INDEX IF NOT EXISTS idx_trips_start_time ON trips (start_time);
