-- How full the vehicle was when the driver sent this fix: one of the GTFS-RT
-- OccupancyStatus names POST /api/v1/locations accepts, or NULL when the
-- report carried none. Every row already here is NULL, so NOT VALID skips only
-- a check those rows would pass: a CHECK added with the column would scan all
-- of location_points while holding its exclusive lock, and stop ingest for as
-- long as that takes. New and updated rows are checked either way.
ALTER TABLE location_points ADD COLUMN occupancy_status TEXT;
ALTER TABLE location_points ADD CONSTRAINT location_points_occupancy_status_check
    CHECK (occupancy_status IN (
        'EMPTY', 'MANY_SEATS_AVAILABLE', 'FEW_SEATS_AVAILABLE', 'STANDING_ROOM_ONLY',
        'CRUSHED_STANDING_ROOM_ONLY', 'FULL', 'NOT_ACCEPTING_PASSENGERS'
    )) NOT VALID;
