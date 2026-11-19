-- When the account last signed in with its password or renewed its session
-- with a refresh token, shown on the admin Users page. NULL until it next
-- does either.
ALTER TABLE users ADD COLUMN last_seen_at TIMESTAMPTZ;

-- Signing in is not an edit to the account, so updated_at stays put when
-- last_seen_at changes. Only UpdateUserLastSeen writes last_seen_at, and it
-- writes nothing else.
DROP TRIGGER set_users_updated_at ON users;
CREATE TRIGGER set_users_updated_at
BEFORE UPDATE ON users
FOR EACH ROW
WHEN (OLD.last_seen_at IS NOT DISTINCT FROM NEW.last_seen_at)
EXECUTE FUNCTION set_users_updated_at();
