package main

import (
	"context"
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
		return fmt.Errorf("bootstrap admin: create: %w", err)
	}
	slog.Info("bootstrapped initial admin user", "email", email)
	return nil
}
