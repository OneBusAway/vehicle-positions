package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TokenRevoker records a token's jti so it is rejected for the rest of its
// lifetime. Kept separate from TokenChecker so the logout handler depends on
// the write and the auth middleware only on the read.
type TokenRevoker interface {
	RevokeToken(ctx context.Context, jti string, userID int64, expiresAt time.Time) error
}

// TokenChecker reports whether a token's jti has been revoked, and whether
// the user a staff token was issued to may still use it.
type TokenChecker interface {
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
	IsUserActive(ctx context.Context, userID int64) (bool, error)
}

// RevokeToken adds a jti to the revocation list. It is idempotent, so logging
// out twice with the same token succeeds both times.
//
// A revocation row deliberately outlives the user it belongs to. user_id is
// ON DELETE SET NULL rather than CASCADE so a revoked jti stays revoked on
// its own terms, whatever happens to the account: checkRevoked also refuses
// a deleted user's tokens, but the blocklist must not depend on that. The
// row's job is to block a jti until it expires, and that job does not end
// when the account does.
//
// expires_at is recorded so revocation rows can be aged out, but nothing
// deletes them yet: this table grows one row per logout. A periodic cleanup
// job (DELETE FROM revoked_tokens WHERE expires_at < NOW()) is needed as a
// follow-up, and is the only thing that should ever remove a row.
func (s *Store) RevokeToken(ctx context.Context, jti string, userID int64, expiresAt time.Time) error {
	err := s.queries.RevokeToken(ctx, db.RevokeTokenParams{
		Jti:       jti,
		UserID:    pgtype.Int8{Int64: userID, Valid: true},
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	return nil
}

// IsTokenRevoked returns true if the jti has been revoked.
func (s *Store) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	revoked, err := s.queries.IsTokenRevoked(ctx, jti)
	if err != nil {
		return false, fmt.Errorf("check token revocation: %w", err)
	}
	return revoked, nil
}

var _ TokenRevoker = (*Store)(nil)
var _ TokenChecker = (*Store)(nil)

// IsUserActive reports whether the user exists and is active. A deleted user
// is not active: the access tokens they were issued outlive the row.
func (s *Store) IsUserActive(ctx context.Context, userID int64) (bool, error) {
	active, err := s.queries.GetUserActive(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check user active: %w", err)
	}
	return active, nil
}
