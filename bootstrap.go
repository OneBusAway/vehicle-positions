package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// adminBootstrapStore is the narrow store interface bootstrapAdmin needs.
type adminBootstrapStore interface {
	CountUsersByRole(ctx context.Context, role string) (int, error)
	CreateUser(ctx context.Context, name, email, password, role string) (*UserResponse, error)
}

// bootstrapAdmin creates the first admin account from ADMIN_BOOTSTRAP_* env
// vars, but only when the users table holds zero admins (spec §4.12).
func bootstrapAdmin(ctx context.Context, store adminBootstrapStore, email, password string) error {
	n, err := store.CountUsersByRole(ctx, "admin")
	if err != nil {
		return fmt.Errorf("bootstrap admin: count: %w", err)
	}
	if n > 0 {
		slog.Info("admin bootstrap skipped: admin users already exist", "count", n)
		return nil
	}
	if err := validatePassword(password); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	if _, err := store.CreateUser(ctx, "Administrator", email, password, "admin"); err != nil {
		// Two instances starting concurrently can both observe zero admins;
		// the users.email unique index makes the INSERT the arbiter. The
		// loser sees a duplicate-email error and must treat it as "already
		// bootstrapped" rather than fail startup.
		if errors.Is(err, ErrDuplicateEmail) {
			slog.Info("admin bootstrap skipped: bootstrap email already exists", "email", email)
			return nil
		}
		return fmt.Errorf("bootstrap admin: create: %w", err)
	}
	slog.Info("bootstrapped initial admin user", "email", email)
	return nil
}
