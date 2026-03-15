package main

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO revoked_tokens (jti, expires_at) VALUES ($1, $2) ON CONFLICT (jti) DO NOTHING`,
		jti, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	return nil
}

func (s *Store) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	var revoked bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM revoked_tokens WHERE jti = $1)`,
		jti,
	).Scan(&revoked)
	if err != nil {
		return false, fmt.Errorf("check token revocation: %w", err)
	}
	return revoked, nil
}
