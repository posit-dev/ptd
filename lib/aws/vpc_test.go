package aws

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeSubnetCIDRs(t *testing.T) {
	// 172.16.0.0/20 with 2 AZs is what the workspaces VPC uses.
	public, private, err := computeSubnetCIDRs("172.16.0.0/20", 2)
	require.NoError(t, err)
	assert.Len(t, public, 2)
	assert.Len(t, private, 2)

	// Private subnets come from the first 3 /22 blocks of a /20.
	// 172.16.0.0/20 → 4 x /22: 172.16.0.0/22, 172.16.4.0/22, 172.16.8.0/22, 172.16.12.0/22
	assert.Equal(t, "172.16.0.0/22", private[0])
	assert.Equal(t, "172.16.4.0/22", private[1])

	// Public subnets come from the 4th /22 (172.16.12.0/22) split into 4 x /24.
	assert.Equal(t, "172.16.12.0/24", public[0])
	assert.Equal(t, "172.16.13.0/24", public[1])
}

func TestComputeSubnetCIDRsInvalidCIDR(t *testing.T) {
	_, _, err := computeSubnetCIDRs("not-a-cidr", 2)
	assert.Error(t, err)
}
