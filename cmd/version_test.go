package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	// Check command registration
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "version" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Version command not registered with root command")
	}

	// Create a buffer to capture command output
	buf := new(bytes.Buffer)
	versionCmd.SetOut(buf)

	// Set a version and run the command
	Version = "1.2.3"
	versionCmd.Run(versionCmd, []string{})

	// Get the output
	output := buf.String()

	// Check that the output contains the version
	if !strings.Contains(output, "PTD CLI 1.2.3") {
		t.Errorf("Expected output to contain 'PTD CLI 1.2.3', got '%s'", output)
	}
}
