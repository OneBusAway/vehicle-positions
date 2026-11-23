-- One row for each change made to a vehicle, an account, a trip or an API
-- key through the admin: who made it, from which address, what it was made
-- to, and the object's fields before and after it. The row is written in the
-- transaction that makes the change, so a change that commits always has its
-- row and one that rolls back never does.
--
-- actor_id is the admin's users.id, or NULL for a change the server made
-- itself: ADMIN_BOOTSTRAP_EMAIL at startup or the reset-admin command. It has
-- no foreign key, because a row has to outlive the account that made it;
-- actor_email keeps who that was. actor_ip is the address the request came
-- from, read as the login rate limiter reads it, '' for the server.
--
-- before and after hold the object's small fields, never a password or a key:
-- NULL before a create, after a delete, and for a new password, which has no
-- field to show.
CREATE TABLE audit_log (
    id          BIGSERIAL   PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_id    BIGINT,
    actor_email TEXT        NOT NULL,
    actor_ip    TEXT        NOT NULL,
    object_type TEXT        NOT NULL,
    object_id   TEXT        NOT NULL,
    action      TEXT        NOT NULL,
    before      JSONB,
    after       JSONB
);

-- The Audit log page lists rows newest first by id, which the primary key
-- serves. This index serves its history of one object, the one view that
-- would otherwise read the whole table to find a few rows.
CREATE INDEX idx_audit_log_object ON audit_log (object_type, object_id, id DESC);

-- A row records what happened, so it is never changed afterwards: not by the
-- server, which has no query that would, and not by a mistaken UPDATE run by
-- hand.
CREATE FUNCTION audit_log_refuse_update()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'audit_log rows cannot be changed';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_log_refuse_update
BEFORE UPDATE ON audit_log
FOR EACH ROW
EXECUTE FUNCTION audit_log_refuse_update();
