package internal

import (
	"os"
	"testing"
)

func TestDataDir(t *testing.T) {
	// Test with XDG_DATA_HOME set
	t.Run("With XDG_DATA_HOME", func(t *testing.T) {
		// Save original environment
		originalXDG, xdgExists := os.LookupEnv("XDG_DATA_HOME")

		// Set test environment
		os.Setenv("XDG_DATA_HOME", "/test/xdg")

		// Test the function
		result := DataDir()

		// Verify result
		if result != "/test/xdg/ptd" {
			t.Errorf("Expected '/test/xdg/ptd', got '%s'", result)
		}

		// Restore environment
		if xdgExists {
			os.Setenv("XDG_DATA_HOME", originalXDG)
		} else {
			os.Unsetenv("XDG_DATA_HOME")
		}
	})

	// Test without XDG_DATA_HOME
	t.Run("Without XDG_DATA_HOME", func(t *testing.T) {
		// Save original environment
		originalXDG, xdgExists := os.LookupEnv("XDG_DATA_HOME")
		originalHOME, homeExists := os.LookupEnv("HOME")

		// Set test environment
		os.Unsetenv("XDG_DATA_HOME")
		os.Setenv("HOME", "/test/home")

		// Test the function
		result := DataDir()

		// Verify result
		if result != "/test/home/.local/share/ptd" {
			t.Errorf("Expected '/test/home/.local/share/ptd', got '%s'", result)
		}

		// Restore environment
		if xdgExists {
			os.Setenv("XDG_DATA_HOME", originalXDG)
		}
		if homeExists {
			os.Setenv("HOME", originalHOME)
		} else {
			os.Unsetenv("HOME")
		}
	})
}
