-- The older server has no viewer role, so each viewer account becomes a
-- deactivated driver, left as Deactivate leaves an account: signed out
-- everywhere and unable to sign in, so it gains nothing it did not have.
-- After the next upgrade an admin can give it its role back and turn it on.
DELETE FROM refresh_tokens WHERE user_id IN (SELECT id FROM users WHERE role = 'viewer');
UPDATE users SET role = 'driver', active = false, sessions_valid_after = NOW() WHERE role = 'viewer';

ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('driver', 'admin'));
