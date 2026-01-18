package pgx

import (
	"context"
	"testing"
	"time"

	"github.com/edgeflare/pigo/internal/testutil/pgtest"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoolManager(t *testing.T) {
	ctx := context.Background()
	cfg := pgtest.ParseConfig(t)
	connString := cfg.ConnString()

	t.Run("NewPoolManager", func(t *testing.T) {
		pm := NewPoolManager()
		require.NotNil(t, pm)
		assert.Empty(t, pm.List())
	})

	t.Run("Add", func(t *testing.T) {
		pm := NewPoolManager()

		// Add first pool
		err := pm.Add(ctx, Pool{
			Name:       "primary",
			ConnString: connString,
		}, true)
		require.NoError(t, err)
		assert.Contains(t, pm.List(), "primary")

		// Add second pool
		err = pm.Add(ctx, Pool{
			Name:       "secondary",
			ConnString: connString,
		})
		require.NoError(t, err)
		assert.Contains(t, pm.List(), "secondary")

		// Try to add duplicate
		err = pm.Add(ctx, Pool{
			Name:       "primary",
			ConnString: connString,
		})
		assert.ErrorIs(t, err, ErrPoolAlreadyExists)

		// Test adding with config
		poolConfig, err := pgxpool.ParseConfig(connString)
		require.NoError(t, err)
		err = pm.Add(ctx, Pool{
			Name:   "config-based",
			Config: poolConfig,
		})
		require.NoError(t, err)
		assert.Contains(t, pm.List(), "config-based")

		t.Cleanup(func() {
			pm.Close()
		})
	})

	t.Run("Get", func(t *testing.T) {
		pm := NewPoolManager()
		err := pm.Add(ctx, Pool{
			Name:       "test-get",
			ConnString: connString,
		})
		require.NoError(t, err)

		pool, err := pm.Get("test-get")
		require.NoError(t, err)
		require.NotNil(t, pool)

		// Test connection works
		err = pool.Ping(ctx)
		require.NoError(t, err)

		// Test non-existent pool
		_, err = pm.Get("nonexistent")
		assert.ErrorIs(t, err, ErrPoolNotFound)

		t.Cleanup(func() {
			pm.Close()
		})
	})

	t.Run("Active", func(t *testing.T) {
		pm := NewPoolManager()

		// Test when no pools exist
		_, err := pm.Active()
		require.Error(t, err)

		// Add pools and test active selection
		err = pm.Add(ctx, Pool{
			Name:       "first",
			ConnString: connString,
		})
		require.NoError(t, err)

		err = pm.Add(ctx, Pool{
			Name:       "second",
			ConnString: connString,
		}, true) // Set as active
		require.NoError(t, err)

		pool, err := pm.Active()
		require.NoError(t, err)
		assert.NotNil(t, pool)

		t.Cleanup(func() {
			pm.Close()
		})
	})

	t.Run("SetActive", func(t *testing.T) {
		pm := NewPoolManager()
		err := pm.Add(ctx, Pool{
			Name:       "pool1",
			ConnString: connString,
		})
		require.NoError(t, err)

		err = pm.Add(ctx, Pool{
			Name:       "pool2",
			ConnString: connString,
		})
		require.NoError(t, err)

		err = pm.SetActive("pool2")
		require.NoError(t, err)

		pool, err := pm.Active()
		require.NoError(t, err)
		require.NotNil(t, pool)

		// Test setting non-existent pool as active
		err = pm.SetActive("nonexistent")
		assert.Error(t, err)

		t.Cleanup(func() {
			pm.Close()
		})
	})

	t.Run("Remove", func(t *testing.T) {
		pm := NewPoolManager()
		err := pm.Add(ctx, Pool{
			Name:       "to-remove",
			ConnString: connString,
		}, true)
		require.NoError(t, err)

		err = pm.Add(ctx, Pool{
			Name:       "keep",
			ConnString: connString,
		})
		require.NoError(t, err)

		// Remove active pool
		err = pm.Remove("to-remove")
		require.NoError(t, err)
		assert.NotContains(t, pm.List(), "to-remove")

		// Test active pool switched
		activePool, err := pm.Active()
		require.NoError(t, err)
		assert.NotNil(t, activePool)

		// Test removing non-existent pool
		err = pm.Remove("nonexistent")
		assert.Error(t, err)

		t.Cleanup(func() {
			pm.Close()
		})
	})

	t.Run("Close", func(t *testing.T) {
		pm := NewPoolManager()
		err := pm.Add(ctx, Pool{
			Name:       "pool1",
			ConnString: connString,
		})
		require.NoError(t, err)

		err = pm.Add(ctx, Pool{
			Name:       "pool2",
			ConnString: connString,
		})
		require.NoError(t, err)

		pm.Close()
		assert.Empty(t, pm.List())

		// Test operations after close
		_, err = pm.Active()
		assert.Error(t, err)
	})

	t.Run("Concurrent Access", func(t *testing.T) {
		pm := NewPoolManager()
		err := pm.Add(ctx, Pool{
			Name:       "concurrent",
			ConnString: connString,
		})
		require.NoError(t, err)

		done := make(chan bool)
		go func() {
			for i := 0; i < 100; i++ {
				pool, err := pm.Get("concurrent")
				if err == nil {
					_ = pool.Ping(ctx)
				}
				time.Sleep(time.Millisecond)
			}
			done <- true
		}()

		go func() {
			for i := 0; i < 100; i++ {
				_ = pm.SetActive("concurrent")
				time.Sleep(time.Millisecond)
			}
			done <- true
		}()

		// Wait for both goroutines
		<-done
		<-done

		t.Cleanup(func() {
			pm.Close()
		})
	})
}
