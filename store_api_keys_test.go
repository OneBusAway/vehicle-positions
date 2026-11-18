package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearAPIKeys(t *testing.T, store *Store) {
	t.Helper()
	_, err := store.pool.Exec(context.Background(), "DELETE FROM api_keys")
	require.NoError(t, err)
}

func TestStore_CreateAndGetAPIKeyByHash(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearAPIKeys(t, store)

	hash := hashAPIKey("round-trip-key")
	created, err := store.CreateAPIKey(ctx, "transit-app", hash)
	require.NoError(t, err)
	require.NotNil(t, created)

	assert.NotZero(t, created.ID)
	assert.Equal(t, "transit-app", created.Name)
	assert.Equal(t, hash, created.KeyHash)
	assert.True(t, created.Active, "new keys are active")
	assert.Nil(t, created.LastUsedAt, "a key that has never been used must be nil, not the zero time")
	assert.False(t, created.CreatedAt.IsZero())

	fetched, err := store.GetAPIKeyByHash(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, fetched)

	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, created.Name, fetched.Name)
	assert.Equal(t, created.KeyHash, fetched.KeyHash)
	assert.Equal(t, created.Active, fetched.Active)
	assert.Nil(t, fetched.LastUsedAt)
	assert.WithinDuration(t, created.CreatedAt, fetched.CreatedAt, 0)
}

func TestStore_GetAPIKeyByHash_NotFound(t *testing.T) {
	store := newTestStore(t)
	clearAPIKeys(t, store)

	key, err := store.GetAPIKeyByHash(context.Background(), hashAPIKey("no-such-key"))

	assert.Nil(t, key)
	assert.True(t, errors.Is(err, ErrAPIKeyNotFound), "an unknown hash must be distinguishable from a DB failure, got %v", err)
}

func TestStore_UpdateAPIKeyLastUsed(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearAPIKeys(t, store)

	hash := hashAPIKey("last-used-key")
	created, err := store.CreateAPIKey(ctx, "feed consumer", hash)
	require.NoError(t, err)
	require.Nil(t, created.LastUsedAt)

	require.NoError(t, store.UpdateAPIKeyLastUsed(ctx, created.ID))

	fetched, err := store.GetAPIKeyByHash(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, fetched.LastUsedAt, "last_used_at must be set after use")
	assert.False(t, fetched.LastUsedAt.Before(fetched.CreatedAt))
}

func TestStore_DeactivateAPIKey(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearAPIKeys(t, store)

	hash := hashAPIKey("revoke-me")
	created, err := store.CreateAPIKey(ctx, "leaving consumer", hash)
	require.NoError(t, err)

	require.NoError(t, store.UpdateAPIKeyLastUsed(ctx, created.ID))
	require.NoError(t, store.DeactivateAPIKey(ctx, created.ID))

	fetched, err := store.GetAPIKeyByHash(ctx, hash)
	require.NoError(t, err)

	assert.False(t, fetched.Active)
	assert.True(t, fetched.UpdatedAt.After(created.UpdatedAt), "revoking must bump updated_at")
	assert.NotNil(t, fetched.LastUsedAt, "revoking keeps last_used_at, which is what an operator investigating the key needs")
}

func TestStore_CountActiveAPIKeys(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearAPIKeys(t, store)
	t.Cleanup(func() { clearAPIKeys(t, store) })

	n, err := store.CountActiveAPIKeys(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "no keys, none active")

	kept, err := store.CreateAPIKey(ctx, "kept", hashAPIKey("kept-key"))
	require.NoError(t, err)
	revoked, err := store.CreateAPIKey(ctx, "revoked", hashAPIKey("revoked-key"))
	require.NoError(t, err)
	require.NoError(t, store.DeactivateAPIKey(ctx, revoked.ID))

	n, err = store.CountActiveAPIKeys(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "a revoked key is not counted")
	require.NotZero(t, kept.ID)
}

func TestStore_DeactivateAPIKey_NotFound(t *testing.T) {
	store := newTestStore(t)
	clearAPIKeys(t, store)

	err := store.DeactivateAPIKey(context.Background(), 999999)

	assert.True(t, errors.Is(err, ErrAPIKeyNotFound), "revoking an unknown id must not report success, got %v", err)
}

// TestStore_ListAPIKeysPage pages through keys that share a created_at, as keys
// inserted in one statement do. The id tiebreaker puts them in one order,
// newest id first, so each key is on exactly one page.
func TestStore_ListAPIKeysPage(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearAPIKeys(t, store)
	t.Cleanup(func() { clearAPIKeys(t, store) })

	_, err := store.pool.Exec(ctx, `
		INSERT INTO api_keys (name, key_hash)
		VALUES ('first', $1), ('second', $2), ('third', $3)`,
		hashAPIKey("first-key"), hashAPIKey("second-key"), hashAPIKey("third-key"))
	require.NoError(t, err)

	names := func(limit, offset int) []string {
		keys, err := store.ListAPIKeysPage(ctx, limit, offset)
		require.NoError(t, err)
		require.NotNil(t, keys, "an empty page must marshal as [], not null")
		out := make([]string, 0, len(keys))
		for _, k := range keys {
			out = append(out, k.Name)
		}
		return out
	}
	assert.Equal(t, []string{"third", "second"}, names(2, 0))
	assert.Equal(t, []string{"first"}, names(2, 2))
	assert.Empty(t, names(2, 4))
}

// TestStore_CreateAPIKey_DuplicateHash covers the UNIQUE constraint on
// key_hash: the second insert must fail and leave nothing behind.
func TestStore_CreateAPIKey_DuplicateHash(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	clearAPIKeys(t, store)

	hash := hashAPIKey("duplicate-key")
	_, err := store.CreateAPIKey(ctx, "original", hash)
	require.NoError(t, err)

	duplicate, err := store.CreateAPIKey(ctx, "copy", hash)
	require.Error(t, err)
	assert.Nil(t, duplicate)

	keys, err := store.ListAPIKeysPage(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, keys, 1, "the rejected insert must not leave a row behind")
	assert.Equal(t, "original", keys[0].Name)
}
