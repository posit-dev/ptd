package azure

import (
	"testing"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/stretchr/testify/assert"
)

func TestValidateUserNodePoolName(t *testing.T) {
	tests := []struct {
		name        string
		poolName    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid lowercase name",
			poolName:    "general",
			expectError: false,
		},
		{
			name:        "valid name with numbers",
			poolName:    "gpu01",
			expectError: false,
		},
		{
			name:        "valid 12 char name",
			poolName:    "verylongname",
			expectError: false,
		},
		{
			name:        "valid 1 char name",
			poolName:    "a",
			expectError: false,
		},
		{
			name:        "invalid empty name",
			poolName:    "",
			expectError: true,
			errorMsg:    "must be 1-12 characters",
		},
		{
			name:        "invalid name too long",
			poolName:    "toolongnamehere",
			expectError: true,
			errorMsg:    "must be 1-12 characters",
		},
		{
			name:        "invalid uppercase name",
			poolName:    "General",
			expectError: true,
			errorMsg:    "must start with lowercase letter",
		},
		{
			name:        "invalid starts with number",
			poolName:    "1general",
			expectError: true,
			errorMsg:    "must start with lowercase letter",
		},
		{
			name:        "invalid contains hyphen",
			poolName:    "gpu-pool",
			expectError: true,
			errorMsg:    "must start with lowercase letter",
		},
		{
			name:        "invalid contains underscore",
			poolName:    "gpu_pool",
			expectError: true,
			errorMsg:    "must start with lowercase letter",
		},
		{
			name:        "reserved name agentpool",
			poolName:    "agentpool",
			expectError: true,
			errorMsg:    "reserved and cannot be used",
		},
		{
			name:        "reserved name userpool",
			poolName:    "userpool",
			expectError: true,
			errorMsg:    "reserved and cannot be used",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUserNodePoolName(tt.poolName)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateUserNodePoolConfig(t *testing.T) {
	tests := []struct {
		name        string
		poolConfig  types.AzureUserNodePoolConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			poolConfig: types.AzureUserNodePoolConfig{
				Name:              "general",
				VMSize:            "Standard_D8s_v6",
				MinCount:          4,
				MaxCount:          10,
				EnableAutoScaling: true,
			},
			expectError: false,
		},
		{
			name: "valid config with initial count",
			poolConfig: types.AzureUserNodePoolConfig{
				Name:              "gpu",
				VMSize:            "Standard_NC4as_T4_v3",
				MinCount:          0,
				MaxCount:          4,
				InitialCount:      func() *int { i := 1; return &i }(),
				EnableAutoScaling: true,
			},
			expectError: false,
		},
		{
			name: "invalid missing VM size",
			poolConfig: types.AzureUserNodePoolConfig{
				Name:              "general",
				MinCount:          4,
				MaxCount:          10,
				EnableAutoScaling: true,
			},
			expectError: true,
			errorMsg:    "vm_size is required",
		},
		{
			name: "invalid pool name",
			poolConfig: types.AzureUserNodePoolConfig{
				Name:              "Invalid_Name",
				VMSize:            "Standard_D8s_v6",
				MinCount:          4,
				MaxCount:          10,
				EnableAutoScaling: true,
			},
			expectError: true,
			errorMsg:    "invalid pool name",
		},
		{
			name: "invalid negative min count",
			poolConfig: types.AzureUserNodePoolConfig{
				Name:              "general",
				VMSize:            "Standard_D8s_v6",
				MinCount:          -1,
				MaxCount:          10,
				EnableAutoScaling: true,
			},
			expectError: true,
			errorMsg:    "min_count must be >= 0",
		},
		{
			name: "invalid max count exceeds limit",
			poolConfig: types.AzureUserNodePoolConfig{
				Name:              "general",
				VMSize:            "Standard_D8s_v6",
				MinCount:          4,
				MaxCount:          1001,
				EnableAutoScaling: true,
			},
			expectError: true,
			errorMsg:    "max_count must be <= 1000",
		},
		{
			name: "invalid min greater than max",
			poolConfig: types.AzureUserNodePoolConfig{
				Name:              "general",
				VMSize:            "Standard_D8s_v6",
				MinCount:          10,
				MaxCount:          5,
				EnableAutoScaling: true,
			},
			expectError: true,
			errorMsg:    "min_count (10) must be <= max_count (5)",
		},
		{
			name: "invalid initial count too low",
			poolConfig: types.AzureUserNodePoolConfig{
				Name:              "general",
				VMSize:            "Standard_D8s_v6",
				MinCount:          0,
				MaxCount:          10,
				InitialCount:      func() *int { i := 0; return &i }(),
				EnableAutoScaling: true,
			},
			expectError: true,
			errorMsg:    "initial_count must be at least 1",
		},
		{
			name: "invalid initial count below min",
			poolConfig: types.AzureUserNodePoolConfig{
				Name:              "general",
				VMSize:            "Standard_D8s_v6",
				MinCount:          5,
				MaxCount:          10,
				InitialCount:      func() *int { i := 3; return &i }(),
				EnableAutoScaling: true,
			},
			expectError: true,
			errorMsg:    "initial_count (3) must be between min_count (5) and max_count (10)",
		},
		{
			name: "invalid initial count above max",
			poolConfig: types.AzureUserNodePoolConfig{
				Name:              "general",
				VMSize:            "Standard_D8s_v6",
				MinCount:          4,
				MaxCount:          10,
				InitialCount:      func() *int { i := 15; return &i }(),
				EnableAutoScaling: true,
			},
			expectError: true,
			errorMsg:    "initial_count (15) must be between min_count (4) and max_count (10)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUserNodePoolConfig(tt.poolConfig)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateUserNodePools(t *testing.T) {
	tests := []struct {
		name        string
		pools       []types.AzureUserNodePoolConfig
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty array is valid",
			pools:       []types.AzureUserNodePoolConfig{},
			expectError: false,
		},
		{
			name: "single valid pool",
			pools: []types.AzureUserNodePoolConfig{
				{
					Name:              "general",
					VMSize:            "Standard_D8s_v6",
					MinCount:          4,
					MaxCount:          10,
					EnableAutoScaling: true,
				},
			},
			expectError: false,
		},
		{
			name: "multiple valid pools",
			pools: []types.AzureUserNodePoolConfig{
				{
					Name:              "general",
					VMSize:            "Standard_D8s_v6",
					MinCount:          4,
					MaxCount:          10,
					EnableAutoScaling: true,
				},
				{
					Name:              "gpu",
					VMSize:            "Standard_NC4as_T4_v3",
					MinCount:          0,
					MaxCount:          4,
					InitialCount:      func() *int { i := 1; return &i }(),
					EnableAutoScaling: true,
				},
			},
			expectError: false,
		},
		{
			name: "duplicate pool names",
			pools: []types.AzureUserNodePoolConfig{
				{
					Name:              "general",
					VMSize:            "Standard_D8s_v6",
					MinCount:          4,
					MaxCount:          10,
					EnableAutoScaling: true,
				},
				{
					Name:              "general",
					VMSize:            "Standard_D4s_v6",
					MinCount:          2,
					MaxCount:          5,
					EnableAutoScaling: true,
				},
			},
			expectError: true,
			errorMsg:    "duplicate pool name: general",
		},
		{
			name: "invalid pool in array",
			pools: []types.AzureUserNodePoolConfig{
				{
					Name:              "general",
					VMSize:            "Standard_D8s_v6",
					MinCount:          4,
					MaxCount:          10,
					EnableAutoScaling: true,
				},
				{
					Name:              "userpool", // reserved name
					VMSize:            "Standard_D4s_v6",
					MinCount:          2,
					MaxCount:          5,
					EnableAutoScaling: true,
				},
			},
			expectError: true,
			errorMsg:    "reserved and cannot be used",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUserNodePools(tt.pools)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
