package secrets

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewSiteSecret(t *testing.T) {
	// Test site names
	testCases := []struct {
		siteName           string
		expectedKeycloakDB string
	}{
		{
			siteName:           "main",
			expectedKeycloakDB: "main_keycloak",
		},
		{
			siteName:           "test-site",
			expectedKeycloakDB: "test_site_keycloak",
		},
		{
			siteName:           "complex-site-name-with-dashes",
			expectedKeycloakDB: "complex_site_name_with_dashes_keycloak",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.siteName, func(t *testing.T) {
			// Create a new site secret
			secret := NewSiteSecret(tc.siteName)

			// Verify the keycloak DB user name is correctly formatted
			assert.Equal(t, tc.expectedKeycloakDB, secret.KeycloakDBUser)

			// Verify random strings are generated with appropriate length
			assert.Equal(t, 30, len(secret.DevDBPassword))
			assert.Equal(t, 30, len(secret.KeycloakDBPassword))
			assert.Equal(t, 30, len(secret.PkgDBPassword))
			assert.Equal(t, 30, len(secret.PubDBPassword))

			// Verify the rskey values are non-empty
			assert.NotEmpty(t, secret.PkgSecretKey)
			assert.NotEmpty(t, secret.PubSecretKey)

			// Verify that the random values are actually random (not reused)
			assert.NotEqual(t, secret.DevDBPassword, secret.KeycloakDBPassword)
			assert.NotEqual(t, secret.DevDBPassword, secret.PkgDBPassword)
			assert.NotEqual(t, secret.DevDBPassword, secret.PubDBPassword)
			assert.NotEqual(t, secret.PkgSecretKey, secret.PubSecretKey)
		})
	}
}

func TestSiteSessionSecret(t *testing.T) {
	// Verify that the SiteSessionSecret type is correctly defined
	var sessionSecret SiteSessionSecret

	// It should be able to store arbitrary key/value pairs
	sessionSecret = make(SiteSessionSecret)
	sessionSecret["key1"] = "value1"
	sessionSecret["key2"] = 123
	sessionSecret["key3"] = true

	assert.Equal(t, "value1", sessionSecret["key1"])
	assert.Equal(t, 123, sessionSecret["key2"])
	assert.Equal(t, true, sessionSecret["key3"])
}

func TestAWSWorkloadSecretStruct(t *testing.T) {
	// Test creating an AWS workload secret
	secret := AWSWorkloadSecret{
		ChronicleBucket:      "test-chronicle-bucket",
		FsDnsName:            "test.fs.example.com",
		FsRootVolumeID:       "fs-12345",
		MainDatabaseID:       "db-12345",
		MainDatabaseURL:      "postgres://user:pass@db.example.com/db",
		PackageManagerBucket: "test-pm-bucket",
		MimirPassword:        "test-password",
	}

	// Verify the fields are set correctly
	assert.Equal(t, "test-chronicle-bucket", secret.ChronicleBucket)
	assert.Equal(t, "test.fs.example.com", secret.FsDnsName)
	assert.Equal(t, "fs-12345", secret.FsRootVolumeID)
	assert.Equal(t, "db-12345", secret.MainDatabaseID)
	assert.Equal(t, "postgres://user:pass@db.example.com/db", secret.MainDatabaseURL)
	assert.Equal(t, "test-pm-bucket", secret.PackageManagerBucket)
	assert.Equal(t, "test-password", secret.MimirPassword)
}

func TestAzureWorkloadSecretStruct(t *testing.T) {
	// Test creating an Azure workload secret
	secret := AzureWorkloadSecret{
		MainDbFqdn: "db.example.azure.com",
	}

	// Verify the field is set correctly
	assert.Equal(t, "db.example.azure.com", secret.MainDbFqdn)
}
