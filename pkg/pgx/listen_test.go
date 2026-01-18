package pgx

/*
import (
	"context"
	"testing"
	"time"

	"github.com/edgeflare/pigo/internal/testutil/pgtest"
	"github.com/stretchr/testify/require"
)

func TestListen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	listenConn := pgtest.Connect(ctx, t)
	defer pgtest.Close(t, listenConn)

	notifyConn := pgtest.Connect(ctx, t)
	defer pgtest.Close(t, notifyConn)

	channelName := "test_channel"

	// Start listening
	notifications, errors := Listen(ctx, listenConn, channelName)

	// Send notification after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond) // Ensure listener is ready
		_, err := notifyConn.Exec(ctx, "NOTIFY "+channelName+", 'test_message'")
		require.NoError(t, err)
	}()

	select {
	case notification := <-notifications:
		require.NotNil(t, notification)
		require.Equal(t, "test_message", notification.Payload)
	case err := <-errors:
		t.Fatalf("Unexpected error: %v", err)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for notification")
	}
}
*/
