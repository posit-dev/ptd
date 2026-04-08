package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEjectCommand(t *testing.T) {
	// Verify the command is registered on rootCmd
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "eject" {
			found = true
			break
		}
	}
	assert.True(t, found, "eject command should be registered on rootCmd")
}

func TestEjectCommand_Use(t *testing.T) {
	assert.Equal(t, "eject <target>", ejectCmd.Use)
}

func TestEjectCommand_RequiresExactlyOneArg(t *testing.T) {
	err := ejectCmd.Args(ejectCmd, []string{})
	assert.Error(t, err, "should reject zero args")

	err = ejectCmd.Args(ejectCmd, []string{"target1", "target2"})
	assert.Error(t, err, "should reject two args")

	err = ejectCmd.Args(ejectCmd, []string{"target1"})
	assert.NoError(t, err, "should accept exactly one arg")
}

func TestEjectCommand_DryRunFlag(t *testing.T) {
	flag := ejectCmd.Flags().Lookup("dry-run")
	require.NotNil(t, flag, "dry-run flag should exist")
	assert.Equal(t, "true", flag.DefValue, "dry-run should default to true")
}

func TestEjectCommand_OutputDirFlag(t *testing.T) {
	flag := ejectCmd.Flags().Lookup("output-dir")
	require.NotNil(t, flag, "output-dir flag should exist")
	assert.Equal(t, "", flag.DefValue, "output-dir should default to empty (computed at runtime)")
}
