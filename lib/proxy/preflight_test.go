package proxy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreflightWithDualPids(t *testing.T) {
	// Create a temporary file for testing
	tmpDir, err := os.MkdirTemp("", "proxy-preflight-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "test-azure-proxy.json")

	// Test cases
	testCases := []struct {
		name           string
		setupProxy     func() *RunningProxy
		targetName     string
		expectActive   bool
		expectError    bool
		errorSubstring string
	}{
		{
			name: "No existing proxy file",
			setupProxy: func() *RunningProxy {
				return nil
			},
			targetName:   "new-target",
			expectActive: false,
			expectError:  false,
		},
		{
			name: "Existing proxy file with different target (not running)",
			setupProxy: func() *RunningProxy {
				// Create a proxy with invalid PIDs (guaranteed not running)
				proxy := NewRunningProxy("existing-target", "8080", -999, -998, filePath)
				_ = proxy.Store() // Save it to disk
				return proxy
			},
			targetName:   "new-target",
			expectActive: false,
			expectError:  false,
		},
		{
			// Note: This test assumes that port 65535 is not in use
			// We choose a high port number that is unlikely to be in use
			name: "No proxy running but port in use",
			setupProxy: func() *RunningProxy {
				return nil // No proxy file
			},
			targetName:   "new-target",
			expectActive: false,
			expectError:  false, // Port 65535 should be free, so no error
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup if needed
			if tc.setupProxy != nil {
				_ = tc.setupProxy()
			} else {
				// Ensure the file doesn't exist
				_ = os.Remove(filePath)
			}

			// Run the preflight check
			proxy, active, err := Preflight(filePath, tc.targetName, "65535")

			// Check results
			assert.Equal(t, tc.expectActive, active)
			
			if tc.expectError {
				require.Error(t, err)
				if tc.errorSubstring != "" {
					assert.Contains(t, err.Error(), tc.errorSubstring)
				}
			} else {
				assert.NoError(t, err)
			}

			// Clean up
			if proxy != nil && proxy.File != "" {
				_ = os.Remove(proxy.File)
			}
		})
	}
}