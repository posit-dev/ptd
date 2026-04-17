package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/posit-dev/ptd/lib/kube"
	"github.com/posit-dev/ptd/lib/types"
	"github.com/spf13/cobra"
)

func TestProxyCommandRegistration(t *testing.T) {
	// Check that the proxy command is registered
	var proxyCmd *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "proxy" {
			proxyCmd = cmd
			break
		}
	}

	if proxyCmd == nil {
		t.Fatal("Proxy command not registered with root command")
	}

	// Check existing flags
	daemonFlag := proxyCmd.PersistentFlags().Lookup("daemon")
	if daemonFlag == nil {
		t.Error("Expected daemon flag to be set")
	} else if daemonFlag.Shorthand != "d" {
		t.Errorf("Expected daemon flag shorthand to be 'd', got '%s'", daemonFlag.Shorthand)
	}

	stopFlag := proxyCmd.PersistentFlags().Lookup("stop")
	if stopFlag == nil {
		t.Error("Expected stop flag to be set")
	} else if stopFlag.Shorthand != "s" {
		t.Errorf("Expected stop flag shorthand to be 's', got '%s'", stopFlag.Shorthand)
	}

	// Check new flags
	listFlag := proxyCmd.Flags().Lookup("list")
	if listFlag == nil {
		t.Error("Expected --list flag to be registered")
	}

	pruneFlag := proxyCmd.Flags().Lookup("prune")
	if pruneFlag == nil {
		t.Error("Expected --prune flag to be registered")
	}

	portFlag := proxyCmd.Flags().Lookup("port")
	if portFlag == nil {
		t.Error("Expected --port flag to be registered")
	}

	// Check that the command accepts 0 or 1 arguments (MaximumNArgs(1))
	if proxyCmd.Args == nil {
		t.Error("Expected Args function to be set")
	}

	// Check that the 'port' subcommand is registered
	var portSubCmd *cobra.Command
	for _, sub := range proxyCmd.Commands() {
		if sub.Name() == "port" {
			portSubCmd = sub
			break
		}
	}
	if portSubCmd == nil {
		t.Error("Expected 'port' subcommand to be registered under proxy")
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
