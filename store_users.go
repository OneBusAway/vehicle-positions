package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// GetUserByEmail fetches the user with this email in any case: both sign-ins,
// reset-admin and the CSV import find an account this way.
// Returns ErrUserNotFound if no user has the email, as none has one Postgres
// can't hold.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	if !storableText(email) {
		return nil, ErrUserNotFound
	}
	row, err := s.queries.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return &User{
		ID:           row.ID,
		Name:         row.Name,
		Email:        row.Email,
		PasswordHash: row.PasswordHash,
		Role:         row.Role,
		Active:       row.Active,
		CreatedAt:    row.CreatedAt.Time,
		UpdatedAt:    row.UpdatedAt.Time,
	}, nil
}

// UpdateUserLastSeen stamps the user's last_seen_at with the current time.
func (s *Store) UpdateUserLastSeen(ctx context.Context, id int64) error {
	if err := s.queries.UpdateUserLastSeen(ctx, id); err != nil {
		return fmt.Errorf("update user last seen: %w", err)
	}
	return nil
}
