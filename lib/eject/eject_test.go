package eject

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/posit-dev/ptd/lib/types/typestest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_CreatesOutputDirectory(t *testing.T) {
	target := newWorkloadTarget("test-workload")
	outputDir := filepath.Join(t.TempDir(), "eject-test-workload")

	err := Run(context.Background(), target, Options{
		TargetName: "test-workload",
		OutputDir:  outputDir,
		DryRun:     true,
	})

	require.NoError(t, err)
	assert.DirExists(t, outputDir)
}

func TestRun_CreatesNestedOutputDirectory(t *testing.T) {
	target := newWorkloadTarget("test-workload")
	outputDir := filepath.Join(t.TempDir(), "nested", "deep", "eject-output")

	err := Run(context.Background(), target, Options{
		TargetName: "test-workload",
		OutputDir:  outputDir,
		DryRun:     true,
	})

	require.NoError(t, err)
	assert.DirExists(t, outputDir)
}

func TestRun_ExistingOutputDirectoryIsNotError(t *testing.T) {
	target := newWorkloadTarget("test-workload")
	outputDir := filepath.Join(t.TempDir(), "eject-existing")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	err := Run(context.Background(), target, Options{
		TargetName: "test-workload",
		OutputDir:  outputDir,
		DryRun:     true,
	})

	require.NoError(t, err)
	assert.DirExists(t, outputDir)
}

func newWorkloadTarget(name string) *typestest.MockTarget {
	mt := &typestest.MockTarget{}
	mt.On("Name").Return(name)
	mt.On("Type").Return(types.TargetTypeWorkload)
	mt.On("ControlRoom").Return(false)
	return mt
}
