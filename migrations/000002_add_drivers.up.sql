CREATE TABLE IF NOT EXISTS drivers (
    id TEXT PRIMARY KEY CHECK (id != ''),
    name TEXT NOT NULL,
    license_number TEXT UNIQUE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS driver_id TEXT REFERENCES drivers(id);