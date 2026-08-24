package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

// UserResponse is the API representation of a user (never includes password_hash).
type UserResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var ErrUserNotFound = errors.New("user not found")
var ErrDuplicateEmail = errors.New("email already exists")

type UserLister interface {
	ListUsers(ctx context.Context) ([]UserResponse, error)
}

type UserGetter interface {
	GetUser(ctx context.Context, id int64) (*UserResponse, error)
}

type UserCreator interface {
	CreateUser(ctx context.Context, name, email, password, role string) (*UserResponse, error)
}

type UserUpdater interface {
	UpdateUser(ctx context.Context, id int64, name, email, role string) (*UserResponse, error)
}

type UserDeleter interface {
	DeleteUser(ctx context.Context, id int64) error
}

func (s *Store) ListUsers(ctx context.Context) ([]UserResponse, error) {
	rows, err := s.queries.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}

	users := make([]UserResponse, 0, len(rows))
	for _, row := range rows {
		users = append(users, UserResponse{
			ID:        row.ID,
			Name:      row.Name,
			Email:     row.Email,
			Role:      row.Role,
			Active:    row.Active,
			CreatedAt: row.CreatedAt.Time,
			UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return users, nil
}

func (s *Store) GetUser(ctx context.Context, id int64) (*UserResponse, error) {
	row, err := s.queries.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user: %w", err)
	}

	return &UserResponse{
		ID:        row.ID,
		Name:      row.Name,
		Email:     row.Email,
		Role:      row.Role,
		Active:    row.Active,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

func (s *Store) CreateUser(ctx context.Context, name, email, password, role string) (*UserResponse, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	row, err := s.queries.CreateUser(ctx, db.CreateUserParams{
		Name:         name,
		Email:        email,
		PasswordHash: string(hash),
		Role:         role,
	})
	if err != nil {
		if isDuplicateEmail(err) {
			return nil, ErrDuplicateEmail
		}
		return nil, fmt.Errorf("create user: %w", err)
	}

	return &UserResponse{
		ID:        row.ID,
		Name:      row.Name,
		Email:     row.Email,
		Role:      row.Role,
		Active:    row.Active,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

// UpdateUser updates a user's name, email, and role.
// TODO: password changes are not supported via this endpoint; add a separate PATCH /password endpoint.
func (s *Store) UpdateUser(ctx context.Context, id int64, name, email, role string) (*UserResponse, error) {
	row, err := s.queries.UpdateUser(ctx, db.UpdateUserParams{
		Name:  name,
		Email: email,
		Role:  role,
		ID:    id,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		if isDuplicateEmail(err) {
			return nil, ErrDuplicateEmail
		}
		return nil, fmt.Errorf("update user: %w", err)
	}

	return &UserResponse{
		ID:        row.ID,
		Name:      row.Name,
		Email:     row.Email,
		Role:      row.Role,
		Active:    row.Active,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

// DeleteUser removes a user from the database by ID.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {

	rowsAffected, err := s.queries.DeleteUser(ctx, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if rowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetUserActive flips a user's active flag. Deactivated users cannot log in.
func (s *Store) SetUserActive(ctx context.Context, id int64, active bool) error {
	rows, err := s.queries.SetUserActive(ctx, db.SetUserActiveParams{ID: id, Active: active})
	if err != nil {
		return fmt.Errorf("set user active: %w", err)
	}
	if rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

// CountUsersByRole returns the total number of users with the given role.
func (s *Store) CountUsersByRole(ctx context.Context, role string) (int, error) {
	n, err := s.queries.CountUsersByRole(ctx, role)
	if err != nil {
		return 0, fmt.Errorf("count users by role: %w", err)
	}
	return int(n), nil
}

// CountActiveUsersByRole returns the number of active users with the given role.
func (s *Store) CountActiveUsersByRole(ctx context.Context, role string) (int, error) {
	n, err := s.queries.CountActiveUsersByRole(ctx, role)
	if err != nil {
		return 0, fmt.Errorf("count active users by role: %w", err)
	}
	return int(n), nil
}

// isDuplicateEmail checks if the error is a PostgreSQL unique violation on the email column.
func isDuplicateEmail(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "email") {
		return true
	}
	return false
}
