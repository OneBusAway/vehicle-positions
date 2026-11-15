-- Every token issued to the user before this instant has been ended by a
-- deactivation or a role change. checkRevoked refuses such a token even after
-- the user is reactivated or given the old role back, which would otherwise
-- let it pass again. NULL means nothing has ended the user's tokens yet.
ALTER TABLE users ADD COLUMN sessions_valid_after TIMESTAMPTZ;
