package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5"
)

// ErrAccountChangedMeanwhile is returned when the account signing in is no
// longer as the sign-in checked it: its password has changed since, or it was
// deactivated or deleted.
var ErrAccountChangedMeanwhile = errors.New("account changed meanwhile")

// signIn signs user in, whose password and active flag were checked as user
// holds them: it runs write, which stores what the sign-in hands out, if
// anything, and records the sign-in, in one transaction under the user lock.
//
// The lock is what a password change, a reset and a deactivation take before
// they end the account's sign-ins. One that came first is seen here, and the
// sign-in is refused with ErrAccountChangedMeanwhile; one that comes after finds
// what write stored, and ends it. signedInAt, read under the lock, is when the
// sign-in's token counts as issued, so that a change that comes after ends that
// token too, whenever it is made.
func (s *Store) signIn(ctx context.Context, user *User, write func(q *db.Queries) error) (signedInAt time.Time, err error) {
	err = s.inTx(ctx, func(q *db.Queries) error {
		account, err := q.GetSignInForUpdate(ctx, user.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAccountChangedMeanwhile
		}
		if err != nil {
			return fmt.Errorf("lock user: %w", err)
		}
		if account.PasswordHash != user.PasswordHash || !account.Active {
			return ErrAccountChangedMeanwhile
		}
		signedInAt = time.Now()
		if write != nil {
			if err := write(q); err != nil {
				return err
			}
		}
		return recordSession(ctx, q, auditSignIn)
	})
	if err != nil {
		return time.Time{}, err
	}
	return signedInAt, nil
}
