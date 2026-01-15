package helpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProcessRunning tests the ProcessRunning function
func TestProcessRunning(t *testing.T) {
	t.Run("negative PID", func(t *testing.T) {
		// Test with negative PID which should always return false
		result := ProcessRunning(-1)
		assert.False(t, result)
	})

	t.Run("zero PID", func(t *testing.T) {
		// Test with zero PID which should always return false
		result := ProcessRunning(0)
		assert.False(t, result)
	})
}

// TestKillProcess tests the KillProcess function
func TestKillProcess(t *testing.T) {
	t.Run("invalid PID", func(t *testing.T) {
		// Attempt to kill a process with an invalid PID
		// This should fail gracefully
		err := KillProcess(-1)
		assert.Error(t, err)
	})
}
