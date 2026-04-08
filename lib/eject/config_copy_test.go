package eject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupWorkloadDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	ptdYaml := `account_id: "123456789012"
region: us-east-1
control_room_account_id: "999888777666"
control_room_cluster_name: main01
control_room_domain: ctrl.example.com
control_room_region: us-west-2
clusters:
  "20240101":
    spec:
      k8s_version: "1.29"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ptd.yaml"), []byte(ptdYaml), 0644))

	siteDir := filepath.Join(dir, "site_main")
	require.NoError(t, os.MkdirAll(siteDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(siteDir, "site.yaml"), []byte("domain: app.example.com\n"), 0644))

	siteDir2 := filepath.Join(dir, "site_secondary")
	require.NoError(t, os.MkdirAll(siteDir2, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(siteDir2, "site.yaml"), []byte("domain: app2.example.com\n"), 0644))

	customDir := filepath.Join(dir, "customizations")
	require.NoError(t, os.MkdirAll(filepath.Join(customDir, "my-step"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(customDir, "manifest.yaml"), []byte("version: 1\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(customDir, "my-step", "main.py"), []byte("print('hello')\n"), 0644))

	return dir
}

func TestCopyWorkloadConfig(t *testing.T) {
	workloadPath := setupWorkloadDir(t)
	outputDir := t.TempDir()

	err := CopyWorkloadConfig(workloadPath, outputDir)
	require.NoError(t, err)

	configDir := filepath.Join(outputDir, "config")
	assert.FileExists(t, filepath.Join(configDir, "ptd.yaml"))
	assert.FileExists(t, filepath.Join(configDir, "site_main", "site.yaml"))
	assert.FileExists(t, filepath.Join(configDir, "site_secondary", "site.yaml"))
	assert.FileExists(t, filepath.Join(configDir, "customizations", "manifest.yaml"))
	assert.FileExists(t, filepath.Join(configDir, "customizations", "my-step", "main.py"))
}

func TestCopyWorkloadConfig_AnnotatesPtdYaml(t *testing.T) {
	workloadPath := setupWorkloadDir(t)
	outputDir := t.TempDir()

	require.NoError(t, CopyWorkloadConfig(workloadPath, outputDir))

	data, err := os.ReadFile(filepath.Join(outputDir, "config", "ptd.yaml"))
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "control_room_account_id: \"999888777666\"  # EJECT: removed during control room severance")
	assert.Contains(t, content, "control_room_domain: ctrl.example.com  # EJECT: removed during control room severance")
	assert.NotContains(t, content, "account_id: \"123456789012\"  # EJECT")
	assert.NotContains(t, content, "region: us-east-1  # EJECT")
}

func TestCopyWorkloadConfig_NoCustomizations(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ptd.yaml"), []byte("region: us-east-1\n"), 0644))
	outputDir := t.TempDir()

	err := CopyWorkloadConfig(dir, outputDir)
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(outputDir, "config", "ptd.yaml"))
	assert.NoDirExists(t, filepath.Join(outputDir, "config", "customizations"))
}

func TestCopyWorkloadConfig_NoSites(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ptd.yaml"), []byte("region: us-east-1\n"), 0644))
	outputDir := t.TempDir()

	err := CopyWorkloadConfig(dir, outputDir)
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(outputDir, "config", "ptd.yaml"))
}

func TestAnnotateControlRoomFields(t *testing.T) {
	input := `account_id: "123"
control_room_account_id: "999"
control_room_domain: ctrl.example.com
region: us-east-1
control_room_region: us-west-2
`
	result := AnnotateControlRoomFields(input)

	assert.Contains(t, result, "control_room_account_id: \"999\"  # EJECT: removed during control room severance")
	assert.Contains(t, result, "control_room_domain: ctrl.example.com  # EJECT: removed during control room severance")
	assert.Contains(t, result, "control_room_region: us-west-2  # EJECT: removed during control room severance")
	assert.Contains(t, result, "account_id: \"123\"\n")
	assert.Contains(t, result, "region: us-east-1\n")
}

func TestAnnotateControlRoomFields_NoControlRoomFields(t *testing.T) {
	input := "account_id: \"123\"\nregion: us-east-1\n"
	assert.Equal(t, input, AnnotateControlRoomFields(input))
}
