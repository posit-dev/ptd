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

func mockConfigLoader(config interface{}) ConfigLoaderFunc {
	return func(t types.Target) (interface{}, error) {
		return config, nil
	}
}

func TestRun_CreatesOutputDirectory(t *testing.T) {
	target := newWorkloadTarget("test-workload")
	outputDir := filepath.Join(t.TempDir(), "eject-test-workload")

	err := Run(context.Background(), target, Options{
		TargetName:   "test-workload",
		OutputDir:    outputDir,
		DryRun:       true,
		ConfigLoader: mockConfigLoader(types.AWSWorkloadConfig{}),
	})

	require.NoError(t, err)
	assert.DirExists(t, outputDir)
}

func TestRun_CreatesNestedOutputDirectory(t *testing.T) {
	target := newWorkloadTarget("test-workload")
	outputDir := filepath.Join(t.TempDir(), "nested", "deep", "eject-output")

	err := Run(context.Background(), target, Options{
		TargetName:   "test-workload",
		OutputDir:    outputDir,
		DryRun:       true,
		ConfigLoader: mockConfigLoader(types.AWSWorkloadConfig{}),
	})

	require.NoError(t, err)
	assert.DirExists(t, outputDir)
}

func TestRun_ExistingOutputDirectoryIsNotError(t *testing.T) {
	target := newWorkloadTarget("test-workload")
	outputDir := filepath.Join(t.TempDir(), "eject-existing")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	err := Run(context.Background(), target, Options{
		TargetName:   "test-workload",
		OutputDir:    outputDir,
		DryRun:       true,
		ConfigLoader: mockConfigLoader(types.AWSWorkloadConfig{}),
	})

	require.NoError(t, err)
	assert.DirExists(t, outputDir)
}

func TestRun_CollectsControlRoomDetails(t *testing.T) {
	target := newWorkloadTarget("test-workload")
	outputDir := filepath.Join(t.TempDir(), "eject-output")

	config := types.AWSWorkloadConfig{
		ControlRoomAccountID: "123456789012",
		ControlRoomDomain:    "ctrl.example.com",
	}

	err := Run(context.Background(), target, Options{
		TargetName:   "test-workload",
		OutputDir:    outputDir,
		DryRun:       true,
		ConfigLoader: mockConfigLoader(config),
	})

	require.NoError(t, err)
}

func TestRun_WritesMetadataJSON(t *testing.T) {
	target := newWorkloadTarget("test-workload")
	outputDir := filepath.Join(t.TempDir(), "eject-output")

	config := types.AWSWorkloadConfig{
		AccountID: "123456789012",
		Region:    "us-east-2",
	}

	err := Run(context.Background(), target, Options{
		TargetName:   "test-workload",
		OutputDir:    outputDir,
		DryRun:       true,
		CLIVersion:   "1.2.3",
		ConfigLoader: mockConfigLoader(config),
	})

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(outputDir, "metadata.json"))
}

func TestRun_WritesReadme(t *testing.T) {
	target := newWorkloadTarget("test-workload")
	outputDir := filepath.Join(t.TempDir(), "eject-output")

	err := Run(context.Background(), target, Options{
		TargetName:   "test-workload",
		OutputDir:    outputDir,
		DryRun:       true,
		ConfigLoader: mockConfigLoader(types.AWSWorkloadConfig{}),
	})

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(outputDir, "README.md"))
}

func TestRun_CopiesWorkloadConfig(t *testing.T) {
	workloadPath := setupWorkloadDir(t)
	target := newWorkloadTarget("test-workload")
	outputDir := filepath.Join(t.TempDir(), "eject-output")

	err := Run(context.Background(), target, Options{
		TargetName:   "test-workload",
		OutputDir:    outputDir,
		DryRun:       true,
		WorkloadPath: workloadPath,
		ConfigLoader: mockConfigLoader(types.AWSWorkloadConfig{}),
	})

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(outputDir, "config", "ptd.yaml"))
	assert.FileExists(t, filepath.Join(outputDir, "config", "site_main", "site.yaml"))
}

func TestRun_SkipsConfigCopyWhenNoWorkloadPath(t *testing.T) {
	target := newWorkloadTarget("test-workload")
	outputDir := filepath.Join(t.TempDir(), "eject-output")

	err := Run(context.Background(), target, Options{
		TargetName:   "test-workload",
		OutputDir:    outputDir,
		DryRun:       true,
		ConfigLoader: mockConfigLoader(types.AWSWorkloadConfig{}),
	})

	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(outputDir, "config"))
}

func newWorkloadTarget(name string) *typestest.MockTarget {
	mt := &typestest.MockTarget{}
	mt.On("Name").Return(name)
	mt.On("Type").Return(types.TargetTypeWorkload)
	mt.On("ControlRoom").Return(false)
	mt.On("CloudProvider").Return(types.AWS)
	return mt
}
