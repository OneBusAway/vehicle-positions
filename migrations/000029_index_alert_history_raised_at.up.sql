-- Supports ListAlertsRaisedBetween, which reads the alerts raised in the days
-- the Analytics page shows. The indexes 000027 made are on cleared_at, which
-- bounds the rows from below only: an alert can clear any time after it was
-- raised, so a period in the past would read every alert cleared since. The
-- table keeps every alert unless LOCATION_RETENTION_PERIOD is set.
-- Not CONCURRENTLY, as in 000023: migrations run before the server takes
-- requests, and alert_history gains a few rows a day per vehicle.
CREATE INDEX IF NOT EXISTS idx_alert_history_raised ON alert_history (raised_at);
