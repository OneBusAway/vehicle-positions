DELETE FROM stop_times st1
USING stop_times st2
WHERE st1.ctid > st2.ctid
 AND st1.trip_id IS NOT DISTINCT FROM st2.trip_id
 AND st1.stop_sequence IS NOT DISTINCT FROM st2.stop_sequence;


ALTER TABLE stop_times
   ADD CONSTRAINT stop_times_trip_id_stop_sequence_key
   UNIQUE (trip_id, stop_sequence);


ALTER TABLE trips
   ADD CONSTRAINT fk_trips_route_id
   FOREIGN KEY (route_id) REFERENCES routes (route_id);


ALTER TABLE stop_times
   ADD CONSTRAINT fk_stop_times_trip_id
   FOREIGN KEY (trip_id) REFERENCES trips (trip_id);


CREATE INDEX idx_stop_times_trip_id ON stop_times (trip_id);
CREATE INDEX idx_trips_route_id ON trips (route_id);
CREATE INDEX idx_location_points_received_at ON location_points (received_at);