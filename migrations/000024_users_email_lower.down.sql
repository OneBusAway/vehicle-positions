-- Emails stay lower-case and renamed duplicates keep their +duplicate
-- addresses: the case they were typed in is gone.
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
DROP INDEX users_email_lower_key;
