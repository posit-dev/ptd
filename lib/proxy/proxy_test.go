package proxy

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRunningProxy(t *testing.T) {
	// Test creating a new running proxy
	targetName := "test-target"
	localPort := "8080"
	pid := 12345
	file := "/tmp/test-proxy.json"

	proxy := NewRunningProxy(targetName, localPort, pid, 0, file)

	// Verify the proxy was created with the correct values
	assert.Equal(t, targetName, proxy.TargetName)
	assert.Equal(t, localPort, proxy.LocalPort)
	assert.Equal(t, pid, proxy.Pid)
	assert.Equal(t, file, proxy.File)

	// Verify the start time was set (approximately)
	assert.WithinDuration(t, time.Now(), proxy.StartTime, 2*time.Second)
}

func TestRunningProxyStoreAndLoad(t *testing.T) {
	// Create a temporary file for testing
	tmpDir, err := os.MkdirTemp("", "proxy-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "test-proxy.json")

	// Create a test proxy
	proxy := NewRunningProxy("test-target", "8080", 12345, 0, filePath)

	// Test storing the proxy to file
	err = proxy.Store()
	require.NoError(t, err)

	// Verify the file exists
	_, err = os.Stat(filePath)
	assert.NoError(t, err)

	// Test loading the proxy from file
	loadedProxy, err := GetRunningProxy(filePath, "test-target")
	require.NoError(t, err)

	// Verify the loaded proxy matches the original
	assert.Equal(t, proxy.TargetName, loadedProxy.TargetName)
	assert.Equal(t, proxy.LocalPort, loadedProxy.LocalPort)
	assert.Equal(t, proxy.Pid, loadedProxy.Pid)
	assert.Equal(t, filePath, loadedProxy.File)
}

func TestRunningProxyStoreWithEmptyFile(t *testing.T) {
	// Test storing a proxy with an empty file path
	proxy := NewRunningProxy("test-target", "8080", 12345, 0, "")

	// This should not cause an error, it just logs and returns
	err := proxy.Store()
	assert.NoError(t, err)
}

func TestRunningProxyDeleteFile(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "proxy-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "test-proxy.json")

	// Store a proxy entry into the registry
	p := NewRunningProxy("test-target", "8080", 12345, 0, filePath)
	err = p.Store()
	require.NoError(t, err)

	// Entry should be present
	loaded, err := GetRunningProxy(filePath, "test-target")
	require.NoError(t, err)
	assert.Equal(t, "test-target", loaded.TargetName)

	// DeleteFile removes the entry from the registry
	err = p.DeleteFile()
	assert.NoError(t, err)

	// Entry should be gone
	after, err := GetRunningProxy(filePath, "test-target")
	require.NoError(t, err)
	assert.Empty(t, after.TargetName, "entry should have been removed from registry")

	// Calling DeleteFile again (entry already gone) should not error
	err = p.DeleteFile()
	assert.NoError(t, err)
}

func TestWorkloadPort(t *testing.T) {
	// Stable: same name always returns the same port
	port1 := WorkloadPort("my-workload")
	port2 := WorkloadPort("my-workload")
	assert.Equal(t, port1, port2, "WorkloadPort should be deterministic")

	// In range [10000, 19999]
	assert.GreaterOrEqual(t, port1, 10000, "Port should be >= 10000")
	assert.LessOrEqual(t, port1, 19999, "Port should be <= 19999")

	// Different names produce different ports (at least for these two)
	portA := WorkloadPort("workload-alpha")
	portB := WorkloadPort("workload-beta")
	assert.NotEqual(t, portA, portB, "Different workload names should produce different ports")

	// Range check for another workload
	assert.GreaterOrEqual(t, portA, 10000)
	assert.LessOrEqual(t, portA, 19999)
}

// Note: The following functions are difficult to test without mocking or using real processes
// - KillProcess: Requires a real process to kill
// - WaitForPortOpen: Hard to reliably test as it depends on network ports
// - IsRunning: Depends on process existence

func TestRunningProxyIsRunningWithInvalidPID(t *testing.T) {
	// Test IsRunning with an invalid PID
	proxy := NewRunningProxy("test-target", "8080", -1, 0, "")

	// An invalid PID should result in IsRunning returning false
	assert.False(t, proxy.IsRunning())
}

func TestRunningProxyWithDualPids(t *testing.T) {
	// Test creating a new running proxy with dual PIDs
	targetName := "test-azure-target"
	localPort := "8080"
	pid := 12345
	pid2 := 12346
	file := "/tmp/test-azure-proxy.json"

	proxy := NewRunningProxy(targetName, localPort, pid, pid2, file)

	// Verify the proxy was created with the correct values
	assert.Equal(t, targetName, proxy.TargetName)
	assert.Equal(t, localPort, proxy.LocalPort)
	assert.Equal(t, pid, proxy.Pid)
	assert.Equal(t, pid2, proxy.Pid2)
	assert.Equal(t, file, proxy.File)
}

// Note: These tests mock the process running checks since we can't easily create real processes for testing

func TestIsRunningWithDualPids(t *testing.T) {
	testCases := []struct {
		name         string
		pid          int
		pid2         int
		expectResult bool
	}{
		{
			name:         "Both PIDs valid (mocked as not running)",
			pid:          -1,
			pid2:         -2,
			expectResult: false,
		},
		{
			name:         "Only primary PID provided",
			pid:          -1,
			pid2:         0,
			expectResult: false,
		},
		{
			name:         "Invalid PIDs (negative values, guaranteed not running)",
			pid:          -1000,
			pid2:         -2000,
			expectResult: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			proxy := NewRunningProxy("test-target", "8080", tc.pid, tc.pid2, "")
			result := proxy.IsRunning()
			assert.Equal(t, tc.expectResult, result)
		})
	}
}

// This is a mock test since we can't actually kill processes in a test environment
func TestKillProcessWithDualPids(t *testing.T) {
	// Test with only primary PID
	proxy1 := NewRunningProxy("test-target", "8080", -1, 0, "")
	err1 := proxy1.KillProcess()
	// Since we're using a fake PID, we expect an error
	require.Error(t, err1)
	assert.Contains(t, err1.Error(), "error killing process")

	// Test with dual PIDs
	// We can't meaningfully test success here without mocking the helpers.KillProcess function
	// But we can ensure the function handles errors appropriately
	proxy2 := NewRunningProxy("test-target", "8080", -1, -2, "")
	err2 := proxy2.KillProcess()
	require.Error(t, err2)
	assert.Contains(t, err2.Error(), "error killing process")
}
