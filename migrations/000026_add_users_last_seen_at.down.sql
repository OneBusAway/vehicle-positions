DROP TRIGGER set_users_updated_at ON users;
CREATE TRIGGER set_users_updated_at
BEFORE UPDATE ON users
FOR EACH ROW
EXECUTE FUNCTION set_users_updated_at();

ALTER TABLE users DROP COLUMN last_seen_at;
