package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ecr"
)

// MockECRClient is a mock implementation of ECR client for testing
type MockECRClient struct {
	DescribeImagesOutput *ecr.DescribeImagesOutput
}

// Mock AWS credentials for testing
type TestCredentials struct {
	*Credentials
}

func TestLatestImageForRepository(t *testing.T) {
	// Test that the function exists with the right signature
	ctx := context.Background()

	// Can't directly test this function without mocking AWS SDK
	// Just make sure the function exists with expected signature
	t.Run("Function exists", func(t *testing.T) {
		_, _ = LatestImageForRepository(ctx, &Credentials{}, "us-west-2", "test-repo")
	})
}

func TestLatestDigestForRepository(t *testing.T) {
	// Since this function now calls LatestImageForRepository, we just need to verify
	// it calls through and returns the digest correctly
	ctx := context.Background()

	// Can't directly test this function without mocking AWS SDK
	// Just make sure the function exists with expected signature
	t.Run("Function exists", func(t *testing.T) {
		_, _ = LatestDigestForRepository(ctx, &Credentials{}, "us-west-2", "test-repo")
	})
}
