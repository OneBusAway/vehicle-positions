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

// A refresh token is the same kind of value as a feed API key: 32 random
// bytes from crypto/rand, presented as a bearer secret and looked up by
// digest. Both therefore share one implementation (api_key_auth.go) rather
// than two that must never diverge — see hashAPIKey there for why SHA-256
// and not bcrypt. These wrappers exist only so the refresh-token call sites
// read in their own vocabulary.
//
// TODO: give the shared pair neutral names (hashOpaqueToken / newOpaqueToken)
// and drop these wrappers. That rename touches the merged API-key code, so it
// belongs in its own PR rather than this one.
func hashRefreshToken(token string) string { return hashAPIKey(token) }

func newRefreshTokenValue() (string, error) { return generateAPIKey() }

// ErrRefreshTokenNotFound is returned when no row matches the presented
// token's hash. It is distinct from a database failure so the refresh handler
// can answer 401 for an unknown token and 500 for an unreachable database.
var ErrRefreshTokenNotFound = errors.New("refresh token not found")

// RefreshToken is a stored refresh token. It deliberately carries no token
// value: only the SHA-256 hash is persisted, so a row can never be turned
// back into a usable credential.
//
// UsedAt is a pointer because "never used" and "used" must be
// distinguishable, and the zero time is a valid timestamp rather than a
// marker for absence.
type RefreshToken struct {
	ID        int64
	UserID    int64
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// RefreshTokenCreator stores a freshly minted refresh token. The login
// handler needs only this.
type RefreshTokenCreator interface {
	CreateRefreshToken(ctx context.Context, tokenHash string, userID int64, expiresAt time.Time) error
}

// RefreshTokenGetter looks up a stored token by hash.
type RefreshTokenGetter interface {
	GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, error)
}

// RefreshTokenRotator consumes a token and stores its replacement.
type RefreshTokenRotator interface {
	RotateRefreshToken(ctx context.Context, usedID int64, newHash string, userID int64, expiresAt time.Time) (bool, error)
}

// RefreshTokenDeleter removes every refresh token belonging to one user.
// Logout uses it so a revoked access token cannot be traded back in for a new
// one.
type RefreshTokenDeleter interface {
	DeleteRefreshTokensForUser(ctx context.Context, userID int64) error
}

// CreateRefreshToken persists the hash of a new refresh token.
func (s *Store) CreateRefreshToken(ctx context.Context, tokenHash string, userID int64, expiresAt time.Time) error {
	err := s.queries.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		TokenHash: tokenHash,
		UserID:    userID,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	return nil
}

// GetRefreshToken returns the stored token with this hash, or
// ErrRefreshTokenNotFound if there is none. Expiry and reuse are reported as
// they are stored; deciding what they mean is the handler's job.
func (s *Store) GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	row, err := s.queries.GetRefreshTokenByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefreshTokenNotFound
		}
		return nil, fmt.Errorf("get refresh token: %w", err)
	}

	token := &RefreshToken{
		ID:        row.ID,
		UserID:    row.UserID,
		ExpiresAt: row.ExpiresAt.Time,
		CreatedAt: row.CreatedAt.Time,
	}
	if row.UsedAt.Valid {
		usedAt := row.UsedAt.Time
		token.UsedAt = &usedAt
	}
	return token, nil
}

// RotateRefreshToken marks usedID consumed and stores newHash in one
// transaction, so a refresh either replaces the client's token or leaves it
// untouched — never burns the old one without handing back a new one.
//
// It reports false when usedID was already consumed. The underlying UPDATE
// filters on used_at IS NULL, which makes consumption a compare-and-set: of
// two concurrent refreshes presenting the same token, exactly one wins and
// the loser is told to re-authenticate rather than both being issued tokens.
func (s *Store) RotateRefreshToken(ctx context.Context, usedID int64, newHash string, userID int64, expiresAt time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	rows, err := qtx.MarkRefreshTokenUsed(ctx, usedID)
	if err != nil {
		return false, fmt.Errorf("mark refresh token used: %w", err)
	}
	if rows == 0 {
		return false, nil
	}

	err = qtx.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		TokenHash: newHash,
		UserID:    userID,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("create rotated refresh token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit refresh token rotation: %w", err)
	}
	return true, nil
}

// DeleteRefreshTokensForUser removes all of a user's refresh tokens.
func (s *Store) DeleteRefreshTokensForUser(ctx context.Context, userID int64) error {
	if err := s.queries.DeleteRefreshTokensForUser(ctx, userID); err != nil {
		return fmt.Errorf("delete refresh tokens for user: %w", err)
	}
	return nil
}

var _ RefreshTokenCreator = (*Store)(nil)
var _ RefreshTokenGetter = (*Store)(nil)
var _ RefreshTokenRotator = (*Store)(nil)
var _ RefreshTokenDeleter = (*Store)(nil)
