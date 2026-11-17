-- Sign-in matches an email in any case and every write stores it lower-case,
-- so one address in two cases can no longer be two accounts. Where it already
-- is, the oldest active account keeps the address, and each other one becomes
-- local+duplicate-<id>@domain. That account still signs in, with that address
-- and its own password, until an admin gives it a real one or deactivates it;
-- nothing is deleted, since trips and assignments point at both.
UPDATE users AS u
SET email = CASE
        WHEN strpos(u.email, '@') > 0
            THEN regexp_replace(lower(u.email), '@([^@]*)$', '+duplicate-' || u.id || '@\1')
        ELSE lower(u.email) || '+duplicate-' || u.id
    END
FROM (
    SELECT id, row_number() OVER (PARTITION BY lower(email) ORDER BY active DESC, id) AS rank
    FROM users
) AS ranked
WHERE u.id = ranked.id AND ranked.rank > 1;

UPDATE users SET email = lower(email) WHERE email <> lower(email);

-- Sign-in looks accounts up by lower(email), so this index serves it, and it
-- makes the plain unique constraint redundant.
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));
ALTER TABLE users DROP CONSTRAINT users_email_key;
