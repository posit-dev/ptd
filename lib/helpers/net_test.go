package helpers

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPortOpen(t *testing.T) {
	// Start a test server on a random available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	// Get the port number
	port := listener.Addr().(*net.TCPAddr).Port

	t.Run("Open port", func(t *testing.T) {
		isOpen := PortOpen("127.0.0.1", string(rune('0'+port/10000%10))+string(rune('0'+port/1000%10))+string(rune('0'+port/100%10))+string(rune('0'+port/10%10))+string(rune('0'+port%10)))
		assert.True(t, isOpen)
	})

	t.Run("Closed port", func(t *testing.T) {
		// Find another port that's likely closed
		var closedPort int
		if port != 12345 {
			closedPort = 12345
		} else {
			closedPort = 12346
		}

		isOpen := PortOpen("127.0.0.1", string(rune('0'+closedPort/10000%10))+string(rune('0'+closedPort/1000%10))+string(rune('0'+closedPort/100%10))+string(rune('0'+closedPort/10%10))+string(rune('0'+closedPort%10)))
		assert.False(t, isOpen)
	})

	t.Run("Invalid host", func(t *testing.T) {
		isOpen := PortOpen("invalid-host-that-doesnt-exist.local", "80")
		assert.False(t, isOpen)
	})
}
