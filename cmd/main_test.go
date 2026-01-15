package main

import (
	"testing"

	"github.com/spf13/viper"
)

func TestRootCommand(t *testing.T) {
	// Test that the root command is properly configured
	if rootCmd.Use != "ptd" {
		t.Errorf("Expected root command use to be 'ptd', got '%s'", rootCmd.Use)
	}

	if rootCmd.Short == "" {
		t.Error("Expected non-empty short description")
	}

	if rootCmd.Long == "" {
		t.Error("Expected non-empty long description")
	}

	// Test that the persistent flags are set correctly
	verboseFlag := rootCmd.PersistentFlags().Lookup("verbose")
	if verboseFlag == nil {
		t.Error("Expected verbose flag to be set")
	}
	if verboseFlag != nil && verboseFlag.Shorthand != "v" {
		t.Errorf("Expected verbose flag shorthand to be 'v', got '%s'", verboseFlag.Shorthand)
	}
}

// We can't easily test setupLogging without modifying the code to make it more testable,
// but we can at least test that it doesn't panic
func TestSetupLogging(t *testing.T) {
	// Test with verbose off
	viper.Set("verbose", false)
	setupLogging()

	// Test with verbose on
	viper.Set("verbose", true)
	setupLogging()
}
