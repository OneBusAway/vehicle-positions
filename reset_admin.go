package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// resetAdminStore is what reset-admin needs from the store.
type resetAdminStore interface {
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	CreateUser(ctx context.Context, name, email, password, role string) (*UserResponse, error)
	UpdateUser(ctx context.Context, id int64, name, email, role string) (*UserResponse, error)
	SetUserPasswordAndRevokeSessions(ctx context.Context, id int64, password string) error
	SetUserActiveAndRevokeSessions(ctx context.Context, id int64, active bool) error
}

// resetAdmin makes email an active admin with password, creating the account
// if there is none. It is the way back in when the only admin has lost their
// password: ADMIN_BOOTSTRAP_* does nothing once an admin exists. Setting the
// password deletes the account's refresh tokens, so a session it already holds
// cannot be renewed; but an access token or admin session cookie already
// issued keeps working until it expires. Signing everyone out at once takes
// rotating JWT_SECRET (docs/operator-manual.md, "Deactivate an account").
func resetAdmin(ctx context.Context, store resetAdminStore, email, password string) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	user, err := store.GetUserByEmail(ctx, email)
	if errors.Is(err, ErrUserNotFound) {
		if _, err := store.CreateUser(ctx, "Administrator", email, password, roleAdmin); err != nil {
			return fmt.Errorf("create admin: %w", err)
		}
		slog.Info("reset-admin: created admin account", "email", email)
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up %s: %w", email, err)
	}
	if user.Role != roleAdmin {
		if _, err := store.UpdateUser(ctx, user.ID, user.Name, user.Email, roleAdmin); err != nil {
			return fmt.Errorf("make %s an admin: %w", email, err)
		}
	}
	if !user.Active {
		if err := store.SetUserActiveAndRevokeSessions(ctx, user.ID, true); err != nil {
			return fmt.Errorf("reactivate %s: %w", email, err)
		}
	}
	if err := store.SetUserPasswordAndRevokeSessions(ctx, user.ID, password); err != nil {
		return fmt.Errorf("set password for %s: %w", email, err)
	}
	slog.Info("reset-admin: reset admin account", "email", email, "was_role", user.Role, "was_active", user.Active)
	return nil
}

// readPassword reads the new password from the first line of r. It comes in
// on stdin rather than as a flag so it never appears in the process list or
// the shell history.
func readPassword(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password: %w", err)
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return "", errors.New("no password on stdin")
	}
	return password, nil
}

// runResetAdmin is the reset-admin subcommand:
//
//	vehicle-positions reset-admin -email ops@example.org < password.txt
//
// It uses the same DATABASE_URL as the server and returns the exit code.
func runResetAdmin(args []string, stdin io.Reader, stderr io.Writer) int {
	fs := flag.NewFlagSet("reset-admin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "email of the admin account to reset or create (required)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: vehicle-positions reset-admin -email ADDRESS < password-file")
		fmt.Fprintln(stderr, "Reads the new password from the first line of stdin and makes ADDRESS an active admin.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *email == "" || fs.NArg() > 0 {
		fs.Usage()
		return 2
	}
	password, err := readPassword(stdin)
	if err != nil {
		fmt.Fprintln(stderr, "reset-admin:", err)
		return 1
	}

	ctx := context.Background()
	databaseURL := envOrDefault("DATABASE_URL", defaultDatabaseURL)
	store, err := NewStore(ctx, databaseURL)
	if err != nil {
		fmt.Fprintln(stderr, "reset-admin:", err)
		return 1
	}
	defer store.Close()
	if err := store.Migrate(databaseURL); err != nil {
		fmt.Fprintln(stderr, "reset-admin:", err)
		return 1
	}
	if err := resetAdmin(ctx, store, *email, password); err != nil {
		fmt.Fprintln(stderr, "reset-admin:", err)
		return 1
	}
	fmt.Fprintf(stderr, "reset-admin: %s is an active admin with the new password\n", *email)
	return 0
}

// warnIfNoActiveAdmin logs a WARN when nobody can sign in to the admin UI, so
// a server started without ADMIN_BOOTSTRAP_* says so instead of starting
// silently with no way in.
func warnIfNoActiveAdmin(ctx context.Context, store interface {
	CountActiveUsersByRole(ctx context.Context, role string) (int, error)
}) {
	n, err := store.CountActiveUsersByRole(ctx, roleAdmin)
	if err != nil {
		slog.Warn("could not count active admins", "error", err)
		return
	}
	if n == 0 {
		slog.Warn("no active admin account: nobody can sign in to the admin UI. " +
			"Set ADMIN_BOOTSTRAP_EMAIL and ADMIN_BOOTSTRAP_PASSWORD on an empty database, " +
			"or run `vehicle-positions reset-admin -email ADDRESS` with the password on stdin")
	}
}

// subcommand runs a named subcommand and exits, if os.Args names one.
func subcommand() {
	if len(os.Args) > 1 && os.Args[1] == "reset-admin" {
		os.Exit(runResetAdmin(os.Args[2:], os.Stdin, os.Stderr))
	}
}
