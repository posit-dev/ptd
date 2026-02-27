package azure

import (
	"fmt"
	"regexp"

	"github.com/posit-dev/ptd/lib/types"
)

// ValidateUserNodePoolName validates that a node pool name meets AKS requirements
// Node pool names must be 1-12 characters, lowercase alphanumeric, and start with a letter
func ValidateUserNodePoolName(name string) error {
	// Must be 1-12 chars, lowercase alphanumeric, start with letter
	// Regex: ^[a-z][a-z0-9]{0,11}$
	if len(name) < 1 || len(name) > 12 {
		return fmt.Errorf("pool name must be 1-12 characters, got %d", len(name))
	}

	matched, err := regexp.MatchString("^[a-z][a-z0-9]{0,11}$", name)
	if err != nil {
		return fmt.Errorf("regex validation error: %w", err)
	}
	if !matched {
		return fmt.Errorf("pool name '%s' must start with lowercase letter and contain only lowercase alphanumeric characters", name)
	}

	// Reserved names that should never be used
	reserved := []string{"agentpool"}
	for _, r := range reserved {
		if name == r {
			return fmt.Errorf("pool name '%s' is reserved and cannot be used", name)
		}
	}

	return nil
}

// ValidateUserNodePoolConfig validates a single user node pool configuration
func ValidateUserNodePoolConfig(pool types.AzureUserNodePoolConfig) error {
	// Validate name
	if err := ValidateUserNodePoolName(pool.Name); err != nil {
		return fmt.Errorf("invalid pool name: %w", err)
	}

	// Validate VMSize is not empty
	if pool.VMSize == "" {
		return fmt.Errorf("pool %s: vm_size is required", pool.Name)
	}

	// Validate count constraints
	if pool.MinCount < 0 {
		return fmt.Errorf("pool %s: min_count must be >= 0, got %d", pool.Name, pool.MinCount)
	}
	if pool.MaxCount > 1000 {
		return fmt.Errorf("pool %s: max_count must be <= 1000, got %d", pool.Name, pool.MaxCount)
	}
	if pool.MinCount > pool.MaxCount {
		return fmt.Errorf("pool %s: min_count (%d) must be <= max_count (%d)", pool.Name, pool.MinCount, pool.MaxCount)
	}

	// Determine initial count
	initialCount := pool.MinCount
	if pool.InitialCount != nil {
		initialCount = *pool.InitialCount
	}

	// Azure requires at least 1 node when creating a pool
	if initialCount < 1 {
		return fmt.Errorf("pool %s: initial_count must be at least 1 (defaults to min_count if not specified), got %d", pool.Name, initialCount)
	}

	// Validate initial count is within range
	if initialCount < pool.MinCount || initialCount > pool.MaxCount {
		return fmt.Errorf("pool %s: initial_count (%d) must be between min_count (%d) and max_count (%d)", pool.Name, initialCount, pool.MinCount, pool.MaxCount)
	}

	return nil
}

// ValidateUserNodePools validates a slice of user node pool configurations
func ValidateUserNodePools(pools []types.AzureUserNodePoolConfig) error {
	if len(pools) == 0 {
		return nil // Empty is valid - will be checked by ResolveUserNodePools
	}

	// Track pool names to detect duplicates
	seen := make(map[string]bool)

	for _, pool := range pools {
		// Validate individual pool configuration
		if err := ValidateUserNodePoolConfig(pool); err != nil {
			return err
		}

		// Check for duplicate names
		if seen[pool.Name] {
			return fmt.Errorf("duplicate pool name: %s", pool.Name)
		}
		seen[pool.Name] = true
	}

	return nil
}
