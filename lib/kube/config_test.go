package kube

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "gopkg.in/yaml.v2"
)

func TestBuildEKSKubeConfig(t *testing.T) {
	endpoint := "https://test.eks.amazonaws.com"
	caCert := "LS0tLS1CRUdJTi..."
	token := "k8s-aws-v1.aHR0cHM6Ly9zdHMuYW1hem..."
	clusterName := "test-cluster"

	config := BuildEKSKubeConfig(endpoint, caCert, token, clusterName)

	assert.Equal(t, "v1", config.APIVersion)
	assert.Equal(t, "Config", config.Kind)
	assert.Equal(t, clusterName, config.CurrentContext)

	// Check cluster
	require.Len(t, config.Clusters, 1)
	assert.Equal(t, clusterName, config.Clusters[0].Name)
	assert.Equal(t, endpoint, config.Clusters[0].Cluster.Server)
	assert.Equal(t, caCert, config.Clusters[0].Cluster.CertificateAuthorityData)

	// Check context
	require.Len(t, config.Contexts, 1)
	assert.Equal(t, clusterName, config.Contexts[0].Name)
	assert.Equal(t, clusterName, config.Contexts[0].Context.Cluster)
	assert.Equal(t, clusterName, config.Contexts[0].Context.User)

	// Check user
	require.Len(t, config.Users, 1)
	assert.Equal(t, clusterName, config.Users[0].Name)
	assert.Equal(t, token, config.Users[0].User.Token)
}

func TestWriteKubeConfig(t *testing.T) {
	tempDir := t.TempDir()
	kubeconfigPath := filepath.Join(tempDir, "kubeconfig")

	config := BuildEKSKubeConfig(
		"https://test.eks.amazonaws.com",
		"LS0tLS1CRUdJTi...",
		"k8s-aws-v1.aHR0cHM6Ly9zdHMuYW1hem...",
		"test-cluster",
	)

	err := WriteKubeConfig(config, kubeconfigPath)
	require.NoError(t, err)

	// Verify file exists and has correct permissions
	info, err := os.Stat(kubeconfigPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	// Verify content is valid YAML
	content, err := os.ReadFile(kubeconfigPath)
	require.NoError(t, err)

	var parsedConfig KubeConfig
	err = yaml.Unmarshal(content, &parsedConfig)
	require.NoError(t, err)
	assert.Equal(t, "test-cluster", parsedConfig.CurrentContext)
}

func TestAddProxyToKubeConfig(t *testing.T) {
	tempDir := t.TempDir()
	kubeconfigPath := filepath.Join(tempDir, "kubeconfig")

	// Create initial kubeconfig without proxy
	config := BuildEKSKubeConfig(
		"https://test.eks.amazonaws.com",
		"LS0tLS1CRUdJTi...",
		"k8s-aws-v1.aHR0cHM6Ly9zdHMuYW1hem...",
		"test-cluster",
	)

	err := WriteKubeConfig(config, kubeconfigPath)
	require.NoError(t, err)

	// Add proxy
	err = AddProxyToKubeConfig(kubeconfigPath, "socks5://localhost:1080")
	require.NoError(t, err)

	// Read and verify proxy was added
	content, err := os.ReadFile(kubeconfigPath)
	require.NoError(t, err)

	var modifiedConfig map[string]interface{}
	err = yaml.Unmarshal(content, &modifiedConfig)
	require.NoError(t, err)

	// Check that proxy-url was added to cluster
	clusters, ok := modifiedConfig["clusters"].([]interface{})
	require.True(t, ok)
	require.Len(t, clusters, 1)

	cluster := clusters[0].(map[interface{}]interface{})
	clusterInfo := cluster["cluster"].(map[interface{}]interface{})
	assert.Equal(t, "socks5://localhost:1080", clusterInfo["proxy-url"])
}