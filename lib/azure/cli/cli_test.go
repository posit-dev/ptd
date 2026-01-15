package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMockMode(t *testing.T) {
	// Enable mock mode
	SetMockMode(true)
	defer SetMockMode(false)

	// Get the Az instance
	az := GetAzInstance()

	// These should succeed in mock mode
	err := az.SetSubscription(context.Background(), "test-subscription")
	assert.NoError(t, err)

	err = az.GetAccessToken(context.Background())
	assert.NoError(t, err)
}
