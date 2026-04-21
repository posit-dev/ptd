package proxy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegistryStoreAndList verifies that Store() upserts into the registry
// and ListRunningProxies() returns all entries.
func TestRegistryStoreAndList(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	registryFile := filepath.Join(tmpDir, "proxies.json")

	p1 := NewRunningProxy("workload-a", "10100", 1001, 0, registryFile)
	p2 := NewRunningProxy("workload-b", "10200", 1002, 0, registryFile)

	require.NoError(t, p1.Store())
	require.NoError(t, p2.Store())

	proxies, err := ListRunningProxies(registryFile)
	require.NoError(t, err)
	assert.Len(t, proxies, 2)

	// Build a map for easier lookup
	byName := make(map[string]*RunningProxy)
	for _, rp := range proxies {
		byName[rp.TargetName] = rp
	}

	require.Contains(t, byName, "workload-a")
	assert.Equal(t, "10100", byName["workload-a"].LocalPort)
	assert.Equal(t, 1001, byName["workload-a"].Pid)

	require.Contains(t, byName, "workload-b")
	assert.Equal(t, "10200", byName["workload-b"].LocalPort)
	assert.Equal(t, 1002, byName["workload-b"].Pid)
}

// TestRegistryDeleteEntry verifies that DeleteFile() removes only the
// target's entry, leaving others intact.
func TestRegistryDeleteEntry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	registryFile := filepath.Join(tmpDir, "proxies.json")

	p1 := NewRunningProxy("keep-me", "10300", 2001, 0, registryFile)
	p2 := NewRunningProxy("remove-me", "10400", 2002, 0, registryFile)

	require.NoError(t, p1.Store())
	require.NoError(t, p2.Store())

	// Remove p2
	require.NoError(t, p2.DeleteFile())

	proxies, err := ListRunningProxies(registryFile)
	require.NoError(t, err)
	assert.Len(t, proxies, 1)
	assert.Equal(t, "keep-me", proxies[0].TargetName)
}

// TestPruneRegistryRemovesDeadPIDs verifies that PruneRegistry removes
// entries with PIDs that are no longer running.
func TestPruneRegistryRemovesDeadPIDs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	registryFile := filepath.Join(tmpDir, "proxies.json")

	// PID -1 is never a valid running process
	dead := NewRunningProxy("dead-workload", "10500", -1, 0, registryFile)
	// Use the current test process's PID — we definitely own it and it is running.
	alivePID := os.Getpid()
	alive := NewRunningProxy("alive-workload", "10600", alivePID, 0, registryFile)

	require.NoError(t, dead.Store())
	require.NoError(t, alive.Store())

	pruned, err := PruneRegistry(registryFile)
	require.NoError(t, err)
	assert.Contains(t, pruned, "dead-workload")
	assert.NotContains(t, pruned, "alive-workload")

	remaining, err := ListRunningProxies(registryFile)
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Equal(t, "alive-workload", remaining[0].TargetName)
}

// TestListRunningProxiesEmptyRegistry verifies that an empty or nonexistent
// registry returns an empty slice without error.
func TestListRunningProxiesEmptyRegistry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	registryFile := filepath.Join(tmpDir, "proxies.json")

	// File does not exist yet
	proxies, err := ListRunningProxies(registryFile)
	require.NoError(t, err)
	assert.Empty(t, proxies)
}

// TestGetRunningProxyNotFound verifies that GetRunningProxy returns an empty
// RunningProxy (not an error) when the target is not in the registry.
func TestGetRunningProxyNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	registryFile := filepath.Join(tmpDir, "proxies.json")

	rp, err := GetRunningProxy(registryFile, "nonexistent")
	require.NoError(t, err)
	assert.Empty(t, rp.TargetName)
	assert.Equal(t, registryFile, rp.File)
}

// TestRegistryUpsertOverwrites verifies that storing the same target twice
// updates the entry rather than duplicating it.
func TestRegistryUpsertOverwrites(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "registry-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	registryFile := filepath.Join(tmpDir, "proxies.json")

	p1 := NewRunningProxy("my-workload", "10700", 3001, 0, registryFile)
	require.NoError(t, p1.Store())

	// Overwrite with a new PID
	p2 := NewRunningProxy("my-workload", "10700", 3999, 0, registryFile)
	require.NoError(t, p2.Store())

	proxies, err := ListRunningProxies(registryFile)
	require.NoError(t, err)
	assert.Len(t, proxies, 1)
	assert.Equal(t, 3999, proxies[0].Pid)
}
