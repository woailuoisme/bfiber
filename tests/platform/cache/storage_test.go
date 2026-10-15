package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/platform/cache"
)

func TestStorage_AsFiberStorage_Nil(t *testing.T) {
	assert.Nil(t, cache.AsFiberStorage(nil))
}

func TestStorage_AsFiberStorage_Passthrough(t *testing.T) {
	raw := cache.NewMemoryStorage()
	defer func() { _ = raw.Close() }()
	assert.Equal(t, raw, cache.AsFiberStorage(raw))
}

func TestStorage_Client_Nil(t *testing.T) {
	var c *cache.Client
	assert.Nil(t, c.Storage())
	assert.Nil(t, c.RDB())
}

func TestStorage_GenericStorage_Contract(t *testing.T) {
	mem := cache.NewMemoryStore()
	defer func() { _ = mem.Close() }()

	storage := cache.AsFiberStorage(mem)
	require.NotNil(t, storage)

	ctx := context.Background()
	key := "ip:127.0.0.1"
	val := []byte("hit_count")

	// Set & Get
	require.NoError(t, storage.Set(key, val, time.Minute))
	got, err := storage.Get(key)
	require.NoError(t, err)
	assert.Equal(t, val, got)

	// Context variants
	gotCtx, err := storage.GetWithContext(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, val, gotCtx)

	// Delete
	require.NoError(t, storage.Delete(key))
	gotDeleted, err := storage.Get(key)
	require.NoError(t, err)
	assert.Nil(t, gotDeleted)

	// Safe edge cases
	assert.NoError(t, storage.Delete(""))
	assert.NoError(t, storage.Set("", nil, 0))
	gotEmpty, err := storage.Get("")
	require.NoError(t, err)
	assert.Nil(t, gotEmpty)

	require.NoError(t, storage.Reset())
	require.NoError(t, storage.ResetWithContext(ctx))
	require.NoError(t, storage.Close())
}

func TestStorage_NewMemoryStorage(t *testing.T) {
	storage := cache.NewMemoryStorage()
	require.NotNil(t, storage)

	require.NoError(t, storage.Set("test_key", []byte("test_val"), time.Minute))
	got, err := storage.Get("test_key")
	require.NoError(t, err)
	assert.Equal(t, []byte("test_val"), got)
	require.NoError(t, storage.Close())
}

func TestStorage_FailOpen(t *testing.T) {
	assert.Nil(t, cache.FailOpen(nil, nil))

	raw := cache.NewMemoryStorage()
	storage := cache.FailOpen(raw, nil)
	require.NotNil(t, storage)

	ctx := context.Background()
	key := "failopen_key"
	val := []byte("failopen_val")

	require.NoError(t, storage.Set(key, val, time.Minute))
	got, err := storage.Get(key)
	require.NoError(t, err)
	assert.Equal(t, val, got)

	gotCtx, err := storage.GetWithContext(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, val, gotCtx)

	require.NoError(t, storage.SetWithContext(ctx, key, val, time.Minute))
	require.NoError(t, storage.DeleteWithContext(ctx, key))
	require.NoError(t, storage.Reset())
	require.NoError(t, storage.ResetWithContext(ctx))
	require.NoError(t, storage.Close())
}
