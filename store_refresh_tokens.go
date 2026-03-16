package main

import (
	"context"
	"fmt"
	"time"
)

type RefreshToken struct {
	ID        string
	UserID    int64
	TokenHash string
	IssuedAt  time.Time
	ExpiresAt time.Time
	Revoked   bool
}

func (s *Store) CreateRefreshToken(ctx context.Context, id string, userID int64, tokenHash string, issuedAt time.Time, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, issued_at, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		id, userID, tokenHash, issuedAt, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	return nil
}

func (s *Store) GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	var rt RefreshToken
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, token_hash, issued_at, expires_at, revoked FROM refresh_tokens WHERE token_hash = $1`,
		tokenHash,
	).Scan(
		&rt.ID,
		&rt.UserID,
		&rt.TokenHash,
		&rt.IssuedAt,
		&rt.ExpiresAt,
		&rt.Revoked,
	)
	if err != nil {
		return nil, fmt.Errorf("get refresh token: %w", err)
	}
	return &rt, nil
}

func (s *Store) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE token_hash = $1`,
		tokenHash,
	)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}
