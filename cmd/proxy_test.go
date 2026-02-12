package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/posit-dev/ptd/lib/kube"
	"github.com/posit-dev/ptd/lib/types"
)

func TestProxyCommandRegistration(t *testing.T) {
	// Check that the proxy command is registered
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "proxy" {
			found = true

			// Check flags
			daemonFlag := cmd.PersistentFlags().Lookup("daemon")
			if daemonFlag == nil {
				t.Error("Expected daemon flag to be set")
			} else if daemonFlag.Shorthand != "d" {
				t.Errorf("Expected daemon flag shorthand to be 'd', got '%s'", daemonFlag.Shorthand)
			}

			stopFlag := cmd.PersistentFlags().Lookup("stop")
			if stopFlag == nil {
				t.Error("Expected stop flag to be set")
			} else if stopFlag.Shorthand != "s" {
				t.Errorf("Expected stop flag shorthand to be 's', got '%s'", stopFlag.Shorthand)
			}

			// Check that the command requires exactly one argument
			if cmd.Args == nil {
				t.Error("Expected Args function to be set")
			}

			break
		}
	}

	if !found {
		t.Error("Proxy command not registered with root command")
	}
}

func TestGetAwsCliPath(t *testing.T) {
	// Test default case (no TOP environment variable)
	originalTOP, topExists := os.LookupEnv("TOP")
	defer func() {
		if topExists {
			os.Setenv("TOP", originalTOP)
		} else {
			os.Unsetenv("TOP")
		}
	}()

	// Test with TOP unset
	os.Unsetenv("TOP")
	pathResult := kube.GetCliPath(types.AWS)
	if pathResult != "aws" {
		t.Errorf("Expected default AWS CLI path to be 'aws', got '%s'", pathResult)
	}

	// Test with TOP set
	os.Setenv("TOP", "/test/path")
	pathResult = kube.GetCliPath(types.AWS)
	expected := filepath.Join("/test/path", ".local/bin/aws")
	if pathResult != expected {
		t.Errorf("Expected AWS CLI path to be '%s', got '%s'", expected, pathResult)
	}
}
