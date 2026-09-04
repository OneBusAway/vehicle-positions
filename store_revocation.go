package main

import (
	"context"
	"fmt"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// TokenRevoker records a token's jti so it is rejected for the rest of its
// lifetime. Kept separate from TokenChecker so the logout handler depends on
// the write and the auth middleware only on the read.
type TokenRevoker interface {
	RevokeToken(ctx context.Context, jti string, userID int64, expiresAt time.Time) error
}

// TokenChecker reports whether a token's jti has been revoked.
type TokenChecker interface {
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
}

// RevokeToken adds a jti to the revocation list. It is idempotent, so logging
// out twice with the same token succeeds both times.
//
// expires_at is recorded so revocation rows can be aged out, but nothing
// deletes them yet: this table grows one row per logout. A periodic cleanup
// job (DELETE FROM revoked_tokens WHERE expires_at < NOW()) is needed as a
// follow-up.
func (s *Store) RevokeToken(ctx context.Context, jti string, userID int64, expiresAt time.Time) error {
	err := s.queries.RevokeToken(ctx, db.RevokeTokenParams{
		Jti:       jti,
		UserID:    userID,
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
