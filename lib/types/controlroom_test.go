package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	yaml "sigs.k8s.io/yaml/goyaml.v3"
)

func TestAWSControlRoomConfigSerialization(t *testing.T) {
	// Create a sample hosted zone ID string
	hostedZoneID := "Z123456789"

	// Create a sample K8s version string
	k8sVersion := "1.24"

	// Create a minimal control room config
	config := AWSControlRoomConfig{
		AccountID:                        "123456789012",
		Region:                           "us-west-2",
		Domain:                           "control.example.com",
		Environment:                      "staging",
		TrueName:                         "control-room-test",
		DBAllocatedStorage:               20,
		DBEngineVersion:                  "13.7",
		DBInstanceClass:                  "db.t3.medium",
		EksK8sVersion:                    &k8sVersion,
		EksNodeGroupMax:                  5,
		EksNodeGroupMin:                  2,
		EksNodeInstanceType:              "t3.large",
		HostedZoneID:                     &hostedZoneID,
		ManageEcrRepositories:            true,
		ProtectPersistentResources:       true,
		TraefikDeploymentReplicas:        2,
		TailscaleEnabled:                 true,
		ExternalDnsVersion:               "0.12.0",
		GrafanaVersion:                   "9.0.0",
		SecretStoreCsiVersion:            "1.2.0",
		SecretStoreCsiAwsProviderVersion: "0.3.0",
		ResourceTags: map[string]string{
			"Environment": "staging",
			"Project":     "ptd",
		},
		TrustedUsers: []TrustedUser{
			{
				Email:      "alice@example.com",
				GivenName:  "Alice",
				FamilyName: "Smith",
				IpAddresses: []TrustedUserIpAddress{
					{
						Ip:      "203.0.113.10",
						Comment: "Office",
					},
					{
						Ip:      "203.0.113.11",
						Comment: "Home",
					},
				},
			},
			{
				Email:      "bob@example.com",
				GivenName:  "Bob",
				FamilyName: "Johnson",
				IpAddresses: []TrustedUserIpAddress{
					{
						Ip:      "198.51.100.20",
						Comment: "VPN",
					},
				},
			},
		},
	}

	// Marshal to YAML
	yamlData, err := yaml.Marshal(config)
	assert.NoError(t, err)

	// Unmarshal from YAML
	var unmarshaledConfig AWSControlRoomConfig
	err = yaml.Unmarshal(yamlData, &unmarshaledConfig)
	assert.NoError(t, err)

	// Verify fields match
	assert.Equal(t, config.AccountID, unmarshaledConfig.AccountID)
	assert.Equal(t, config.Region, unmarshaledConfig.Region)
	assert.Equal(t, config.Domain, unmarshaledConfig.Domain)
	assert.Equal(t, config.Environment, unmarshaledConfig.Environment)
	assert.Equal(t, config.TrueName, unmarshaledConfig.TrueName)
	assert.Equal(t, config.DBAllocatedStorage, unmarshaledConfig.DBAllocatedStorage)
	assert.Equal(t, config.DBEngineVersion, unmarshaledConfig.DBEngineVersion)
	assert.Equal(t, config.DBInstanceClass, unmarshaledConfig.DBInstanceClass)

	// Check pointers
	assert.NotNil(t, unmarshaledConfig.EksK8sVersion)
	assert.Equal(t, *config.EksK8sVersion, *unmarshaledConfig.EksK8sVersion)
	assert.NotNil(t, unmarshaledConfig.HostedZoneID)
	assert.Equal(t, *config.HostedZoneID, *unmarshaledConfig.HostedZoneID)

	// Check maps
	assert.Equal(t, config.ResourceTags["Environment"], unmarshaledConfig.ResourceTags["Environment"])
	assert.Equal(t, config.ResourceTags["Project"], unmarshaledConfig.ResourceTags["Project"])

	// Check booleans
	assert.Equal(t, config.ManageEcrRepositories, unmarshaledConfig.ManageEcrRepositories)
	assert.Equal(t, config.ProtectPersistentResources, unmarshaledConfig.ProtectPersistentResources)
	assert.Equal(t, config.TailscaleEnabled, unmarshaledConfig.TailscaleEnabled)

	// Check TrustedUsers
	assert.Equal(t, len(config.TrustedUsers), len(unmarshaledConfig.TrustedUsers))
	assert.Equal(t, config.TrustedUsers[0].Email, unmarshaledConfig.TrustedUsers[0].Email)
	assert.Equal(t, config.TrustedUsers[0].GivenName, unmarshaledConfig.TrustedUsers[0].GivenName)
	assert.Equal(t, config.TrustedUsers[0].FamilyName, unmarshaledConfig.TrustedUsers[0].FamilyName)
	assert.Equal(t, len(config.TrustedUsers[0].IpAddresses), len(unmarshaledConfig.TrustedUsers[0].IpAddresses))
	assert.Equal(t, config.TrustedUsers[0].IpAddresses[0].Ip, unmarshaledConfig.TrustedUsers[0].IpAddresses[0].Ip)
	assert.Equal(t, config.TrustedUsers[0].IpAddresses[0].Comment, unmarshaledConfig.TrustedUsers[0].IpAddresses[0].Comment)
	assert.Equal(t, config.TrustedUsers[1].Email, unmarshaledConfig.TrustedUsers[1].Email)
}
