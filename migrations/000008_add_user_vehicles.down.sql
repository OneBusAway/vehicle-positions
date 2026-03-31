-- Revert CASCADE FK constraints back to simple references
ALTER TABLE user_vehicles DROP CONSTRAINT IF EXISTS user_vehicles_user_id_fkey;
ALTER TABLE user_vehicles DROP CONSTRAINT IF EXISTS user_vehicles_vehicle_id_fkey;

ALTER TABLE user_vehicles
    ADD CONSTRAINT user_vehicles_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);

ALTER TABLE user_vehicles
    ADD CONSTRAINT user_vehicles_vehicle_id_fkey FOREIGN KEY (vehicle_id) REFERENCES vehicles(id);

-- Remove index and column added by this migration
DROP INDEX IF EXISTS idx_user_vehicles_vehicle_id;
ALTER TABLE user_vehicles DROP COLUMN IF EXISTS created_at;
