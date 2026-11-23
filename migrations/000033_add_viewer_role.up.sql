-- A viewer signs in to the admin and sees every page, but is refused every
-- change: the admin gates let a viewer through for GET and HEAD only, and the
-- driver API refuses one outright.
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('driver', 'admin', 'viewer'));
