-- Add created_at column to existing user_vehicles table (created in 000006)
ALTER TABLE user_vehicles ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Add vehicle_id index for reverse lookups
CREATE INDEX IF NOT EXISTS idx_user_vehicles_vehicle_id ON user_vehicles(vehicle_id);

-- Drop unnamed FK constraints and re-add with explicit names and CASCADE
ALTER TABLE user_vehicles DROP CONSTRAINT IF EXISTS user_vehicles_user_id_fkey;
ALTER TABLE user_vehicles DROP CONSTRAINT IF EXISTS user_vehicles_vehicle_id_fkey;

ALTER TABLE user_vehicles
    ADD CONSTRAINT user_vehicles_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE user_vehicles
    ADD CONSTRAINT user_vehicles_vehicle_id_fkey FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE;
