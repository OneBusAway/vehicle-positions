package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

// ErrPasswordChangedMeanwhile is returned when an account's password is no
// longer the one the change was checked against.
var ErrPasswordChangedMeanwhile = errors.New("password changed meanwhile")

// passwordChange is an account setting its own new password.
type passwordChange struct {
	UserID int64
	// CheckedHash is the hash the current password was checked against.
	CheckedHash string
	NewPassword string
	// KeepTokenHash is the refresh token of the sign-in that makes the change,
	// the one that goes on.
	KeepTokenHash string
}

// OwnPasswordChanger is what the password change handler needs.
type OwnPasswordChanger interface {
	GetUserCredentials(ctx context.Context, id int64) (email, passwordHash string, err error)
	ChangeOwnPassword(ctx context.Context, change passwordChange) error
}

// GetUserCredentials returns the email and the password hash of the account.
// Returns ErrUserNotFound if there is none.
func (s *Store) GetUserCredentials(ctx context.Context, id int64) (email, passwordHash string, err error) {
	row, err := s.queries.GetUserCredentials(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrUserNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("get user credentials: %w", err)
	}
	return row.Email, row.PasswordHash, nil
}

// ChangeOwnPassword sets the new password and ends every other sign-in of the
// account at once, in one transaction under the user lock: it deletes every
// refresh token but the one to keep, and ends every access token issued before
// the change, the caller's own with the rest, which its kept refresh token
// replaces. An admin's reset, SetUserPasswordAndRevokeSessions, keeps none and
// lets the access tokens run out.
//
// Returns ErrPasswordChangedMeanwhile when the password is not the one that was
// checked, and ErrRefreshTokenNotFound when the token to keep is not this
// account's, or is spent or expired: the sign-in making the change would end
// with the rest. Nothing changes then.
func (s *Store) ChangeOwnPassword(ctx context.Context, change passwordChange) error {
	// Hashed before the transaction opens, so the user lock is never held
	// across bcrypt.
	hash, err := bcrypt.GenerateFromPassword([]byte(change.NewPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackTx(tx)
	q := s.queries.WithTx(tx)

	// Locked first, as each revocation does: a refresh in flight commits before
	// this reads the token, or waits for it.
	current, err := q.GetUserPasswordForUpdate(ctx, change.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	// Read under the lock, as a refresh reads the time its token counts as issued
	// at, and from the same clock: every refresh is before this or after it.
	now := time.Now()
	if current != change.CheckedHash {
		return ErrPasswordChangedMeanwhile
	}

	kept, err := q.GetRefreshTokenByHash(ctx, change.KeepTokenHash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("get refresh token: %w", err)
	}
	if err != nil || kept.UserID != change.UserID || kept.UsedAt.Valid || !now.Before(kept.ExpiresAt.Time) {
		return ErrRefreshTokenNotFound
	}

	if err := q.ChangeOwnPassword(ctx, db.ChangeOwnPasswordParams{
		ID:           change.UserID,
		PasswordHash: string(hash),
		EndedAt:      pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	if err := q.DeleteOtherRefreshTokensForUser(ctx, db.DeleteOtherRefreshTokensForUserParams{
		UserID: change.UserID, TokenHash: change.KeepTokenHash,
	}); err != nil {
		return fmt.Errorf("delete other refresh tokens: %w", err)
	}
	// As for a reset, no fields before or after.
	if err := recordAudit(ctx, q, auditChange{ObjectType: auditUser, ObjectID: strconv.FormatInt(change.UserID, 10), Action: auditPassword}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}
