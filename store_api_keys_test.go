package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

/**
* This file contains tests for the Store's API key management functions, including creating API keys, retrieving them by hash, and updating their last used timestamps. The tests cover typical use cases as well as edge cases like inactive keys.
 */
func TestStore_CreateAndGetAPIKeyByHash(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM api_keys")
	require.NoError(t, err)

	rawKey := "test-feed-key-123"
	keyHash := hashAPIKey(rawKey)

	created, err := store.CreateAPIKey(ctx, "Test Feed Consumer", keyHash, true)
	require.NoError(t, err)
	require.NotNil(t, created)

	got, err := store.GetAPIKeyByHash(ctx, keyHash)
	require.NoError(t, err)

	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "Test Feed Consumer", got.Name)
	assert.Equal(t, keyHash, got.KeyHash)
	assert.True(t, got.Active)
	assert.Nil(t, got.LastUsedAt)
}

func TestStore_UpdateAPIKeyLastUsed(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM api_keys")
	require.NoError(t, err)

	keyHash := hashAPIKey("last-used-key")
	created, err := store.CreateAPIKey(ctx, "Last Used Test", keyHash, true)
	require.NoError(t, err)

	err = store.UpdateAPIKeyLastUsed(ctx, created.ID)
	require.NoError(t, err)

	got, err := store.GetAPIKeyByHash(ctx, keyHash)
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
}

func TestStore_GetAPIKeyByHash_Inactive(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "DELETE FROM api_keys")
	require.NoError(t, err)

	keyHash := hashAPIKey("inactive-key")
	_, err = store.CreateAPIKey(ctx, "Inactive Key", keyHash, false)
	require.NoError(t, err)

	got, err := store.GetAPIKeyByHash(ctx, keyHash)
	require.NoError(t, err)
	assert.False(t, got.Active)
}
