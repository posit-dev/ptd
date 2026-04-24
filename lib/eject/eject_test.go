package eject

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/posit-dev/ptd/lib/types/typestest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
		HandoffCollector: func(ctx context.Context, t types.Target, opts Options, crDetails *ControlRoomDetails) error {
			return nil
		},
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

var noopHandoffCollector = func(ctx context.Context, t types.Target, opts Options, crDetails *ControlRoomDetails) error {
	return nil
}

func newWorkloadTarget(name string) *typestest.MockTarget {
	mt := &typestest.MockTarget{}
	mt.On("Name").Return(name)
	mt.On("Type").Return(types.TargetTypeWorkload)
	mt.On("ControlRoom").Return(false)
	mt.On("CloudProvider").Return(types.AWS)
	return mt
}

func TestRun_Eject_StripsConfigAndDeletesMimir(t *testing.T) {
	workloadDir := t.TempDir()
	ptdYaml := filepath.Join(workloadDir, "ptd.yaml")
	os.WriteFile(ptdYaml, []byte(`spec:
  account_id: "111111111111"
  region: us-east-2
  control_room_account_id: "999999999999"
  control_room_cluster_name: main01-prod
  control_room_domain: ctrl.posit.team
  control_room_region: us-east-1
`), 0644)

	target := newWorkloadTarget("test-workload")
	creds := typestest.DefaultCredentials()
	target.On("Credentials", mock.Anything).Return(creds, nil)

	// Set up control room target with a fake secret store containing the Mimir secret
	secretName := "ctrl-prod.mimir-auth.posit.team"
	initialSecret, _ := json.Marshal(map[string]string{"test-workload": "pass123", "other": "pass456"})
	store := &fakeSecretStore{secrets: map[string]string{secretName: string(initialSecret)}}

	crTarget := &typestest.MockTarget{}
	crTarget.On("Name").Return("ctrl-prod")
	crTarget.On("Credentials", mock.Anything).Return(creds, nil)
	crTarget.On("SecretStore").Return(store)

	outputDir := filepath.Join(t.TempDir(), "eject-output")

	err := Run(context.Background(), target, Options{
		TargetName:        "test-workload",
		OutputDir:         outputDir,
		DryRun:            false,
		WorkloadPath:      workloadDir,
		ControlRoomTarget: crTarget,
		ConfigLoader: mockConfigLoader(types.AWSWorkloadConfig{
			AccountID:              "111111111111",
			Region:                 "us-east-2",
			ControlRoomAccountID:   "999999999999",
			ControlRoomClusterName: "main01-prod",
			ControlRoomDomain:      "ctrl.posit.team",
			ControlRoomRegion:      "us-east-1",
		}),
		HandoffCollector: noopHandoffCollector,
	})

	require.NoError(t, err)

	// Verify config was stripped
	strippedData, err := os.ReadFile(ptdYaml)
	require.NoError(t, err)
	assert.Contains(t, string(strippedData), `control_room_account_id: ""`)
	assert.Contains(t, string(strippedData), `control_room_domain: ""`)

	// Verify Mimir secret was updated (test-workload entry removed)
	var remaining map[string]string
	json.Unmarshal([]byte(store.secrets[secretName]), &remaining)
	assert.NotContains(t, remaining, "test-workload")
	assert.Contains(t, remaining, "other")

	// Verify eject record was written
	assert.FileExists(t, filepath.Join(outputDir, "eject-record.json"))
	recordData, _ := os.ReadFile(filepath.Join(outputDir, "eject-record.json"))
	var record EjectRecord
	require.NoError(t, json.Unmarshal(recordData, &record))
	assert.True(t, record.ConfigStripped)
	assert.True(t, record.MimirSecretRemoved)
	assert.NotEmpty(t, record.EjectedAt)
	assert.NotEmpty(t, record.ControlRoomSnapshot.Fields)
}

func TestRun_Eject_FailsPreflightOnEmptyConfig(t *testing.T) {
	workloadDir := t.TempDir()
	os.WriteFile(filepath.Join(workloadDir, "ptd.yaml"), []byte("spec:\n  region: us-east-2\n"), 0644)

	target := newWorkloadTarget("test-workload")
	creds := typestest.DefaultCredentials()
	target.On("Credentials", mock.Anything).Return(creds, nil)

	outputDir := filepath.Join(t.TempDir(), "eject-output")

	err := Run(context.Background(), target, Options{
		TargetName:   "test-workload",
		OutputDir:    outputDir,
		DryRun:       false,
		WorkloadPath: workloadDir,
		ConfigLoader: mockConfigLoader(types.AWSWorkloadConfig{}),
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "preflight checks did not pass")
}

func TestRun_Eject_NoControlRoomTarget(t *testing.T) {
	workloadDir := t.TempDir()
	os.WriteFile(filepath.Join(workloadDir, "ptd.yaml"), []byte(`spec:
  control_room_domain: ctrl.posit.team
  control_room_region: us-east-1
`), 0644)

	target := newWorkloadTarget("test-workload")
	creds := typestest.DefaultCredentials()
	target.On("Credentials", mock.Anything).Return(creds, nil)

	outputDir := filepath.Join(t.TempDir(), "eject-output")

	err := Run(context.Background(), target, Options{
		TargetName:        "test-workload",
		OutputDir:         outputDir,
		DryRun:            false,
		WorkloadPath:      workloadDir,
		ControlRoomTarget: nil,
		ConfigLoader: mockConfigLoader(types.AWSWorkloadConfig{
			ControlRoomDomain: "ctrl.posit.team",
			ControlRoomRegion: "us-east-1",
		}),
		HandoffCollector: noopHandoffCollector,
	})

	// Should succeed — Mimir removal is skipped, config is still stripped
	require.NoError(t, err)

	strippedData, _ := os.ReadFile(filepath.Join(workloadDir, "ptd.yaml"))
	assert.Contains(t, string(strippedData), `control_room_domain: ""`)

	recordData, _ := os.ReadFile(filepath.Join(outputDir, "eject-record.json"))
	var record EjectRecord
	json.Unmarshal(recordData, &record)
	assert.True(t, record.ConfigStripped)
	assert.False(t, record.MimirSecretRemoved)
}

func TestRun_Eject_MimirDeletionFailsContinues(t *testing.T) {
	workloadDir := t.TempDir()
	ptdYaml := filepath.Join(workloadDir, "ptd.yaml")
	os.WriteFile(ptdYaml, []byte(`spec:
  control_room_account_id: "999999999999"
  control_room_domain: ctrl.posit.team
  control_room_region: us-east-1
`), 0644)

	target := newWorkloadTarget("test-workload")
	creds := typestest.DefaultCredentials()
	target.On("Credentials", mock.Anything).Return(creds, nil)

	store := &failingSecretStore{}
	crTarget := &typestest.MockTarget{}
	crTarget.On("Name").Return("ctrl-prod")
	crTarget.On("Credentials", mock.Anything).Return(creds, nil)
	crTarget.On("SecretStore").Return(store)

	outputDir := filepath.Join(t.TempDir(), "eject-output")

	err := Run(context.Background(), target, Options{
		TargetName:        "test-workload",
		OutputDir:         outputDir,
		DryRun:            false,
		WorkloadPath:      workloadDir,
		ControlRoomTarget: crTarget,
		ConfigLoader: mockConfigLoader(types.AWSWorkloadConfig{
			ControlRoomAccountID: "999999999999",
			ControlRoomDomain:    "ctrl.posit.team",
			ControlRoomRegion:    "us-east-1",
		}),
		HandoffCollector: noopHandoffCollector,
	})

	require.NoError(t, err)

	// Config should still be stripped even though Mimir failed
	strippedData, _ := os.ReadFile(ptdYaml)
	assert.Contains(t, string(strippedData), `control_room_domain: ""`)

	// Eject record should reflect partial success
	recordData, _ := os.ReadFile(filepath.Join(outputDir, "eject-record.json"))
	var record EjectRecord
	require.NoError(t, json.Unmarshal(recordData, &record))
	assert.True(t, record.ConfigStripped)
	assert.False(t, record.MimirSecretRemoved)
}

type failingSecretStore struct{}

func (f *failingSecretStore) SecretExists(_ context.Context, _ types.Credentials, _ string) bool {
	return true
}
func (f *failingSecretStore) GetSecretValue(_ context.Context, _ types.Credentials, _ string) (string, error) {
	return "", fmt.Errorf("access denied")
}
func (f *failingSecretStore) PutSecretValue(_ context.Context, _ types.Credentials, _ string, _ string) error {
	return fmt.Errorf("access denied")
}
func (f *failingSecretStore) CreateSecret(_ context.Context, _ types.Credentials, _ string, _ string) error {
	return nil
}
func (f *failingSecretStore) CreateSecretIfNotExists(_ context.Context, _ types.Credentials, _ string, _ any) error {
	return nil
}
func (f *failingSecretStore) EnsureWorkloadSecret(_ context.Context, _ types.Credentials, _ string, _ any) error {
	return nil
}

func TestRun_DryRun_SkipsDestructiveSteps(t *testing.T) {
	workloadDir := t.TempDir()
	ptdYaml := filepath.Join(workloadDir, "ptd.yaml")
	originalConfig := `spec:
  control_room_account_id: "999999999999"
  control_room_domain: ctrl.posit.team
  control_room_region: us-east-1
`
	os.WriteFile(ptdYaml, []byte(originalConfig), 0644)

	target := newWorkloadTarget("test-workload")

	secretName := "ctrl-prod.mimir-auth.posit.team"
	initialSecret, _ := json.Marshal(map[string]string{"test-workload": "pass123"})
	store := &fakeSecretStore{secrets: map[string]string{secretName: string(initialSecret)}}

	crTarget := &typestest.MockTarget{}
	crTarget.On("Name").Return("ctrl-prod")
	crTarget.On("SecretStore").Return(store)

	outputDir := filepath.Join(t.TempDir(), "eject-output")

	err := Run(context.Background(), target, Options{
		TargetName:        "test-workload",
		OutputDir:         outputDir,
		DryRun:            true,
		WorkloadPath:      workloadDir,
		ControlRoomTarget: crTarget,
		ConfigLoader: mockConfigLoader(types.AWSWorkloadConfig{
			ControlRoomAccountID: "999999999999",
			ControlRoomDomain:    "ctrl.posit.team",
			ControlRoomRegion:    "us-east-1",
		}),
		HandoffCollector: noopHandoffCollector,
	})

	require.NoError(t, err)

	// Config must NOT be stripped
	data, _ := os.ReadFile(ptdYaml)
	assert.Contains(t, string(data), `control_room_domain: ctrl.posit.team`)

	// Mimir secret must NOT be touched
	var remaining map[string]string
	json.Unmarshal([]byte(store.secrets[secretName]), &remaining)
	assert.Contains(t, remaining, "test-workload")

	// No eject record should be written
	assert.NoFileExists(t, filepath.Join(outputDir, "eject-record.json"))
}
