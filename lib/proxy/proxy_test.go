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
	loadedProxy, err := GetRunningProxy(filePath)
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
	// Create a temporary file for testing
	tmpDir, err := os.MkdirTemp("", "proxy-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "test-proxy.json")

	// Create a test file
	err = os.WriteFile(filePath, []byte("test"), 0644)
	require.NoError(t, err)

	// Create a proxy with this file
	proxy := NewRunningProxy("test-target", "8080", 12345, 0, filePath)

	// Delete the file
	err = proxy.DeleteFile()
	assert.NoError(t, err)

	// Verify the file no longer exists
	_, err = os.Stat(filePath)
	assert.True(t, os.IsNotExist(err))

	// Deleting a non-existent file should not return an error
	err = proxy.DeleteFile()
	assert.NoError(t, err)
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
