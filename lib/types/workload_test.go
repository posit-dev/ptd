package types

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	yaml "sigs.k8s.io/yaml/goyaml.v3"
)

func TestSiteConfigSerialization(t *testing.T) {
	site := SiteConfig{
		ZoneID:                "Z123456789",
		Domain:                "example.com",
		DomainType:            "public",
		UseTraefikForwardAuth: true,
	}

	// Marshal to YAML
	yamlData, err := yaml.Marshal(site)
	assert.NoError(t, err)

	// Unmarshal from YAML
	var unmarshaledSite SiteConfig
	err = yaml.Unmarshal(yamlData, &unmarshaledSite)
	assert.NoError(t, err)

	// Verify fields match
	assert.Equal(t, site.ZoneID, unmarshaledSite.ZoneID)
	assert.Equal(t, site.Domain, unmarshaledSite.Domain)
	assert.Equal(t, site.DomainType, unmarshaledSite.DomainType)
	assert.Equal(t, site.UseTraefikForwardAuth, unmarshaledSite.UseTraefikForwardAuth)
}

func TestAWSWorkloadConfigSerialization(t *testing.T) {
	// Create sample external ID
	externalID := uuid.New()

	// Create a minimal workload config
	config := AWSWorkloadConfig{
		AccountID:          "123456789012",
		Region:             "us-west-2",
		ExternalID:         &externalID,
		TailscaleEnabled:   true,
		KeycloakEnabled:    true,
		VpcCidr:            "10.0.0.0/16",
		VpcAzCount:         3,
		PublicLoadBalancer: true,
		ResourceTags: map[string]string{
			"Environment": "test",
			"Owner":       "team",
		},
		Sites: map[string]SiteConfig{
			"main": {
				ZoneID:                "Z123456789",
				Domain:                "example.com",
				DomainType:            "public",
				UseTraefikForwardAuth: true,
			},
		},
		Clusters: map[string]AWSWorkloadClusterConfig{
			"main": {
				ClusterName:      "test-cluster",
				NodeGroupName:    "test-node-group",
				NodeInstanceType: "t3.medium",
				K8sVersion:       "1.24",
				NodeGroupMinSize: 1,
				NodeGroupMaxSize: 3,
				SubnetIDs:        []string{"subnet-1", "subnet-2"},
			},
		},
	}

	// Marshal to YAML
	yamlData, err := yaml.Marshal(config)
	assert.NoError(t, err)

	// Unmarshal from YAML
	var unmarshaledConfig AWSWorkloadConfig
	err = yaml.Unmarshal(yamlData, &unmarshaledConfig)
	assert.NoError(t, err)

	// Verify fields match
	assert.Equal(t, config.AccountID, unmarshaledConfig.AccountID)
	assert.Equal(t, config.Region, unmarshaledConfig.Region)
	assert.Equal(t, *config.ExternalID, *unmarshaledConfig.ExternalID)
	assert.Equal(t, config.TailscaleEnabled, unmarshaledConfig.TailscaleEnabled)
	assert.Equal(t, config.KeycloakEnabled, unmarshaledConfig.KeycloakEnabled)

	// Check nested structures
	assert.Equal(t, config.Sites["main"].Domain, unmarshaledConfig.Sites["main"].Domain)
	assert.Equal(t, config.Clusters["main"].ClusterName, unmarshaledConfig.Clusters["main"].ClusterName)
	assert.Equal(t, config.Clusters["main"].NodeInstanceType, unmarshaledConfig.Clusters["main"].NodeInstanceType)
}

func TestAzureWorkloadConfigSerialization(t *testing.T) {
	// Create a minimal Azure workload config
	config := AzureWorkloadConfig{
		SubscriptionID:          "123456789-abcd-efgh-ijkl-1234567890ab",
		TenantID:                "abcdefgh-1234-5678-ijkl-1234567890ab",
		Region:                  "eastus",
		ClientID:                "12345678-abcd-efgh-ijkl-1234567890ab",
		SecretsProviderClientID: "98765432-abcd-efgh-ijkl-1234567890ab",
		InstanceType:            "Standard_D4s_v3",
		ControlPlaneNodeCount:   3,
		WorkerNodeCount:         2,
		DBStorageSizeGB:         20,
		ResourceTags: map[string]string{
			"Environment": "test",
			"Owner":       "team",
		},
		Sites: map[string]SiteConfig{
			"main": {
				ZoneID:                "Z123456789",
				Domain:                "example.com",
				DomainType:            "public",
				UseTraefikForwardAuth: true,
			},
		},
		Clusters: map[string]AzureWorkloadClusterConfig{
			"main": {
				KubernetesVersion:    "1.24.0",
				PublicEndpointAccess: false,
				Components: AzureWorkloadClusterComponentConfig{
					SecretStoreCsiDriverAzureProviderVersion: "1.0.0",
				},
			},
		},
	}

	// Marshal to YAML
	yamlData, err := yaml.Marshal(config)
	assert.NoError(t, err)

	// Unmarshal from YAML
	var unmarshaledConfig AzureWorkloadConfig
	err = yaml.Unmarshal(yamlData, &unmarshaledConfig)
	assert.NoError(t, err)

	// Verify fields match
	assert.Equal(t, config.SubscriptionID, unmarshaledConfig.SubscriptionID)
	assert.Equal(t, config.TenantID, unmarshaledConfig.TenantID)
	assert.Equal(t, config.Region, unmarshaledConfig.Region)
	assert.Equal(t, config.ClientID, unmarshaledConfig.ClientID)
	assert.Equal(t, config.InstanceType, unmarshaledConfig.InstanceType)

	// Check nested structures
	assert.Equal(t, config.Sites["main"].Domain, unmarshaledConfig.Sites["main"].Domain)
	assert.Equal(t, config.Clusters["main"].KubernetesVersion, unmarshaledConfig.Clusters["main"].KubernetesVersion)
	assert.Equal(t, config.Clusters["main"].Components.SecretStoreCsiDriverAzureProviderVersion,
		unmarshaledConfig.Clusters["main"].Components.SecretStoreCsiDriverAzureProviderVersion)
}

func TestAzureWorkloadConfigBastionInstanceType(t *testing.T) {
	// Test that bastion_instance_type field is properly serialized/deserialized
	config := AzureWorkloadConfig{
		SubscriptionID:          "123456789-abcd-efgh-ijkl-1234567890ab",
		TenantID:                "abcdefgh-1234-5678-ijkl-1234567890ab",
		Region:                  "eastus",
		ClientID:                "12345678-abcd-efgh-ijkl-1234567890ab",
		SecretsProviderClientID: "98765432-abcd-efgh-ijkl-1234567890ab",
		BastionInstanceType:     "Standard_B2s",
		InstanceType:            "Standard_D4s_v3",
		ControlPlaneNodeCount:   1,
		WorkerNodeCount:         1,
		DBStorageSizeGB:         20,
	}

	// Marshal to YAML
	yamlData, err := yaml.Marshal(config)
	assert.NoError(t, err)

	// Verify the YAML contains the bastion_instance_type field
	yamlString := string(yamlData)
	assert.Contains(t, yamlString, "bastion_instance_type: Standard_B2s")

	// Unmarshal from YAML
	var unmarshaledConfig AzureWorkloadConfig
	err = yaml.Unmarshal(yamlData, &unmarshaledConfig)
	assert.NoError(t, err)

	// Verify bastion_instance_type matches
	assert.Equal(t, config.BastionInstanceType, unmarshaledConfig.BastionInstanceType)
	assert.Equal(t, "Standard_B2s", unmarshaledConfig.BastionInstanceType)
}

func TestAzureWorkloadConfigBastionInstanceTypeFromYAML(t *testing.T) {
	// Test parsing bastion_instance_type from YAML
	yamlContent := `
subscription_id: "123456789-abcd-efgh-ijkl-1234567890ab"
tenant_id: "abcdefgh-1234-5678-ijkl-1234567890ab"
region: "eastus"
client_id: "12345678-abcd-efgh-ijkl-1234567890ab"
secrets_provider_client_id: "98765432-abcd-efgh-ijkl-1234567890ab"
bastion_instance_type: "Standard_D2s_v3"
instance_type: "Standard_D4s_v3"
control_plane_node_count: 1
worker_node_count: 1
db_storage_size_gb: 20
`

	var config AzureWorkloadConfig
	err := yaml.Unmarshal([]byte(yamlContent), &config)
	assert.NoError(t, err)
	assert.Equal(t, "Standard_D2s_v3", config.BastionInstanceType)
}

func TestAzureUserNodePoolConfigSerialization(t *testing.T) {
	initialCount := 5
	maxPods := 50
	osDiskSizeGB := 256

	poolConfig := AzureUserNodePoolConfig{
		Name:              "testpool",
		VMSize:            "Standard_D8s_v6",
		MinCount:          4,
		MaxCount:          10,
		InitialCount:      &initialCount,
		EnableAutoScaling: true,
		AvailabilityZones: []string{"1", "2", "3"},
		NodeTaints:        []string{"workload=gpu:NoSchedule"},
		NodeLabels: map[string]string{
			"gpu": "true",
		},
		MaxPods:      &maxPods,
		RootDiskSize: &osDiskSizeGB,
	}

	// Marshal to YAML
	yamlData, err := yaml.Marshal(poolConfig)
	assert.NoError(t, err)

	// Unmarshal from YAML
	var unmarshaledPool AzureUserNodePoolConfig
	err = yaml.Unmarshal(yamlData, &unmarshaledPool)
	assert.NoError(t, err)

	// Verify fields match
	assert.Equal(t, poolConfig.Name, unmarshaledPool.Name)
	assert.Equal(t, poolConfig.VMSize, unmarshaledPool.VMSize)
	assert.Equal(t, poolConfig.MinCount, unmarshaledPool.MinCount)
	assert.Equal(t, poolConfig.MaxCount, unmarshaledPool.MaxCount)
	assert.Equal(t, *poolConfig.InitialCount, *unmarshaledPool.InitialCount)
	assert.Equal(t, poolConfig.EnableAutoScaling, unmarshaledPool.EnableAutoScaling)
	assert.Equal(t, poolConfig.AvailabilityZones, unmarshaledPool.AvailabilityZones)
	assert.Equal(t, poolConfig.NodeTaints, unmarshaledPool.NodeTaints)
	assert.Equal(t, poolConfig.NodeLabels, unmarshaledPool.NodeLabels)
	assert.Equal(t, *poolConfig.MaxPods, *unmarshaledPool.MaxPods)
	assert.Equal(t, *poolConfig.RootDiskSize, *unmarshaledPool.RootDiskSize)
}

func TestResolveUserNodePools_WithPools(t *testing.T) {
	// Cluster with user_node_pools defined
	config := AzureWorkloadClusterConfig{
		KubernetesVersion:          "1.28.0",
		SystemNodePoolInstanceType: "Standard_D2s_v6",
		UserNodePools: []AzureUserNodePoolConfig{
			{
				Name:              "general",
				VMSize:            "Standard_D8s_v6",
				MinCount:          4,
				MaxCount:          10,
				EnableAutoScaling: true,
			},
		},
	}

	pools, err := config.ResolveUserNodePools()
	assert.NoError(t, err)
	assert.Len(t, pools, 1)
	assert.Equal(t, "general", pools[0].Name)
}

func TestResolveUserNodePools_WithoutPools(t *testing.T) {
	// Cluster without user_node_pools should error
	config := AzureWorkloadClusterConfig{
		KubernetesVersion:          "1.28.0",
		SystemNodePoolInstanceType: "Standard_D2s_v6",
	}

	pools, err := config.ResolveUserNodePools()
	assert.Error(t, err)
	assert.Nil(t, pools)
	assert.Contains(t, err.Error(), "user_node_pools must be defined in cluster configuration")
}
