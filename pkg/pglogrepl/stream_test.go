package pglogrepl

import (
	"context"
	"testing"
	"time"

	"github.com/edgeflare/pigo/internal/testutil/pgtest"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	testConn := pgtest.Connect(ctx, t)

	// clean any existing test objects
	_, err := testConn.Exec(ctx, `
		DROP PUBLICATION IF EXISTS test_pub;
		SELECT pg_terminate_backend(active_pid) 
		FROM pg_replication_slots 
		WHERE slot_name = 'test_slot' AND active_pid IS NOT NULL;
		SELECT pg_drop_replication_slot(slot_name) 
		FROM pg_replication_slots 
		WHERE slot_name = 'test_slot';
	`)
	require.NoError(t, err)

	// create replication connection
	connConfig := pgtest.ParseConfig(t)
	connConfig.RuntimeParams["replication"] = "database"
	replConn, err := pgx.ConnectConfig(ctx, connConfig)
	require.NoError(t, err)
	t.Cleanup(func() {
		// cleanup replication resources
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		pgtest.Close(t, replConn)

		_, err := testConn.Exec(cleanupCtx, `
			DROP TABLE IF EXISTS test_stream;
			DROP PUBLICATION IF EXISTS test_pub;
			SELECT pg_terminate_backend(active_pid) 
			FROM pg_replication_slots 
			WHERE slot_name = 'test_slot' AND active_pid IS NOT NULL;
			SELECT pg_drop_replication_slot(slot_name) 
			FROM pg_replication_slots 
			WHERE slot_name = 'test_slot';
		`)
		require.NoError(t, err)
	})

	// test table
	_, err = testConn.Exec(ctx, `
		DROP TABLE IF EXISTS test_stream;
		CREATE TABLE test_stream (
			id SERIAL PRIMARY KEY,
			name TEXT,
			created_at TIMESTAMPTZ DEFAULT NOW()
		);
	`)
	require.NoError(t, err)

	// Set replica identity to full for complete UPDATE/DELETE info
	_, err = testConn.Exec(ctx, "ALTER TABLE test_stream REPLICA IDENTITY FULL")
	require.NoError(t, err)

	// Configure streaming
	cfg := &Config{
		Publication:           "test_pub",
		Slot:                  "test_slot",
		Tables:                []string{"test_stream"},
		BufferSize:            100,
		StandbyUpdateInterval: time.Second,
	}

	// Start streaming
	events, err := Stream(ctx, replConn.PgConn(), cfg)
	require.NoError(t, err)

	// give it a sec for replication to be fully set up
	time.Sleep(500 * time.Millisecond)

	// channel to collect received events
	received := make(chan cdc.Event, 100)
	go func() {
		for event := range events {
			received <- event
		}
	}()

	// Begin transaction for consistent testing
	tx, err := testConn.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	// Test INSERT
	_, err = tx.Exec(ctx, "INSERT INTO test_stream (name) VALUES ($1)", "test1")
	require.NoError(t, err)

	// Commit to ensure the change is visible to replication
	err = tx.Commit(ctx)
	require.NoError(t, err)

	select {
	case event := <-received:
		require.Equal(t, cdc.OpCreate, event.Payload.Op)
		require.Equal(t, "test_stream", event.Payload.Source.Table)
		require.NotNil(t, event.Payload.After)
		require.Nil(t, event.Payload.Before)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for INSERT event")
	}

	// Test UPDATE
	tx, err = testConn.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "UPDATE test_stream SET name = $1 WHERE name = $2", "test2", "test1")
	require.NoError(t, err)
	err = tx.Commit(ctx)
	require.NoError(t, err)

	select {
	case event := <-received:
		require.Equal(t, cdc.OpUpdate, event.Payload.Op)
		require.Equal(t, "test_stream", event.Payload.Source.Table)
		require.NotNil(t, event.Payload.Before)
		require.NotNil(t, event.Payload.After)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for UPDATE event")
	}

	// Test DELETE
	tx, err = testConn.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "DELETE FROM test_stream WHERE name = $1", "test2")
	require.NoError(t, err)
	err = tx.Commit(ctx)
	require.NoError(t, err)

	select {
	case event := <-received:
		require.Equal(t, cdc.OpDelete, event.Payload.Op)
		require.Equal(t, "test_stream", event.Payload.Source.Table)
		require.NotNil(t, event.Payload.Before)
		require.Nil(t, event.Payload.After)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for DELETE event")
	}

	// Test TRUNCATE
	tx, err = testConn.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "TRUNCATE test_stream")
	require.NoError(t, err)
	err = tx.Commit(ctx)
	require.NoError(t, err)

	select {
	case event := <-received:
		require.Equal(t, cdc.OpTruncate, event.Payload.Op)
		require.Equal(t, "test_stream", event.Payload.Source.Table)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for TRUNCATE event")
	}
}
