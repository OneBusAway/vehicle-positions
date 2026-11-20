-- One row for each service day the Analytics page has added up, written once
-- the day is settled and older than yesterday, so a period's past days are
-- read here instead of being worked out again from every trail.
--
-- totals holds what the page adds up over a period and nothing more: for each
-- route with service that day, its trips scheduled and run and its stops on
-- time, early, late and with no report, by the hour of the service day the
-- trips leave in; and how many trips reached their last stop in each minute
-- early or late. on_time_early_s and on_time_late_s are the on-time window the
-- day was judged by: a row judged by another window is worked out again.
CREATE TABLE service_day_totals (
    service_date    DATE        PRIMARY KEY,
    on_time_early_s INTEGER     NOT NULL,
    on_time_late_s  INTEGER     NOT NULL,
    totals          JSONB       NOT NULL,
    written_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
