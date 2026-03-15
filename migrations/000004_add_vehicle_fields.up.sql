-- Defaults ('', true) apply to vehicles auto-created by the location ingest path (UpsertVehicle).
ALTER TABLE vehicles ADD COLUMN agency_tag TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN active BOOLEAN NOT NULL DEFAULT true;
