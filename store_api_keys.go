package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5"
)

// ErrAPIKeyNotFound is returned when no api_keys row matches the lookup.
var ErrAPIKeyNotFound = errors.New("api key not found")

// APIKeyStore is the read path the feed middleware needs: look a key up by
// its hash and record that it was used.
type APIKeyStore interface {
	GetAPIKeyByHash(ctx context.Context, keyHash string) (*APIKey, error)
	UpdateAPIKeyLastUsed(ctx context.Context, id int64) error
}

// APIKeyManager is the admin CRUD surface. It is kept separate from
// APIKeyStore so the feed middleware cannot create or revoke keys.
type APIKeyManager interface {
	CreateAPIKey(ctx context.Context, name, keyHash string) (*APIKey, error)
	ListAPIKeys(ctx context.Context) ([]APIKey, error)
	DeactivateAPIKey(ctx context.Context, id int64) error
}

var (
	_ APIKeyStore   = (*Store)(nil)
	_ APIKeyManager = (*Store)(nil)
)

// GetAPIKeyByHash returns the key with the given hash, active or not, so
// callers can tell a revoked key from an unknown one.
func (s *Store) GetAPIKeyByHash(ctx context.Context, keyHash string) (*APIKey, error) {
	row, err := s.queries.GetAPIKeyByHash(ctx, keyHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAPIKeyNotFound
		}
		return nil, fmt.Errorf("get api key by hash: %w", err)
	}
	key := toAPIKey(row)
	return &key, nil
}

// UpdateAPIKeyLastUsed stamps the key's last_used_at with the current time.
func (s *Store) UpdateAPIKeyLastUsed(ctx context.Context, id int64) error {
	if err := s.queries.UpdateAPIKeyLastUsed(ctx, id); err != nil {
		return fmt.Errorf("update api key last used: %w", err)
	}
	return nil
}

// CreateAPIKey stores a new key. keyHash is the hash of the raw key; the raw
// key itself never reaches the database.
func (s *Store) CreateAPIKey(ctx context.Context, name, keyHash string) (*APIKey, error) {
	row, err := s.queries.CreateAPIKey(ctx, db.CreateAPIKeyParams{Name: name, KeyHash: keyHash})
	if err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}
	key := toAPIKey(row)
	return &key, nil
}

// ListAPIKeys returns all keys, newest first.
func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.queries.ListAPIKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}

	keys := make([]APIKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, toAPIKey(row))
	}
	return keys, nil
}

// DeactivateAPIKey revokes a key by clearing its active flag. The row is kept
// so last_used_at survives the revocation, which is exactly what an operator
// investigating a key wants to see.
func (s *Store) DeactivateAPIKey(ctx context.Context, id int64) error {
	rows, err := s.queries.DeactivateAPIKey(ctx, id)
	if err != nil {
		return fmt.Errorf("deactivate api key: %w", err)
	}
	if rows == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

// toAPIKey maps a DB row to the API type. created_at and updated_at are NOT
// NULL in the schema, so .Valid is always true; last_used_at is nullable and
// stays nil until the key is first used.
func toAPIKey(row db.ApiKey) APIKey {
	key := APIKey{
		ID:        row.ID,
		Name:      row.Name,
		KeyHash:   row.KeyHash,
		Active:    row.Active,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}
	if row.LastUsedAt.Valid {
		lastUsed := row.LastUsedAt.Time
		key.LastUsedAt = &lastUsed
	}
	return key
}
