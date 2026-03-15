CREATE TABLE stops (
   stop_id TEXT PRIMARY KEY,
   stop_name TEXT,
   stop_lat DOUBLE PRECISION,
   stop_lon DOUBLE PRECISION
);


CREATE TABLE routes (
   route_id TEXT PRIMARY KEY,
   route_short_name TEXT,
   route_long_name TEXT
);


CREATE TABLE trips (
   trip_id TEXT PRIMARY KEY,
   route_id TEXT,
   service_id TEXT
);


CREATE TABLE stop_times (
   trip_id TEXT,
   arrival_time TEXT,
   departure_time TEXT,
   stop_id TEXT,
   stop_sequence INTEGER
);
