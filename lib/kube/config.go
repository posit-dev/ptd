package kube

import (
	"fmt"
	"os"

	yaml "gopkg.in/yaml.v2"
)

// KubeConfig represents the kubeconfig YAML structure
type KubeConfig struct {
	APIVersion     string         `yaml:"apiVersion"`
	Kind           string         `yaml:"kind"`
	Clusters       []NamedCluster `yaml:"clusters"`
	Contexts       []NamedContext `yaml:"contexts"`
	CurrentContext string         `yaml:"current-context"`
	Users          []NamedUser    `yaml:"users"`
}

// NamedCluster represents a named cluster in kubeconfig
type NamedCluster struct {
	Name    string  `yaml:"name"`
	Cluster Cluster `yaml:"cluster"`
}

// Cluster represents cluster configuration
type Cluster struct {
	Server                   string `yaml:"server"`
	CertificateAuthorityData string `yaml:"certificate-authority-data"`
	ProxyURL                 string `yaml:"proxy-url,omitempty"`
}

// NamedContext represents a named context in kubeconfig
type NamedContext struct {
	Name    string  `yaml:"name"`
	Context Context `yaml:"context"`
}

// Context represents context configuration
type Context struct {
	Cluster string `yaml:"cluster"`
	User    string `yaml:"user"`
}

// NamedUser represents a named user in kubeconfig
type NamedUser struct {
	Name string `yaml:"name"`
	User User   `yaml:"user"`
}

// User represents user configuration
type User struct {
	Token string      `yaml:"token,omitempty"`
	Exec  *ExecConfig `yaml:"exec,omitempty"`
}

// ExecConfig represents a client-go exec credential plugin
type ExecConfig struct {
	APIVersion      string   `yaml:"apiVersion"`
	Command         string   `yaml:"command"`
	Args            []string `yaml:"args,omitempty"`
	InteractiveMode string   `yaml:"interactiveMode,omitempty"`
}

// BuildEKSKubeConfig builds a KubeConfig for an EKS cluster
func BuildEKSKubeConfig(endpoint, caCert, token, clusterName string) KubeConfig {
	return KubeConfig{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: []NamedCluster{
			{
				Name: clusterName,
				Cluster: Cluster{
					Server:                   endpoint,
					CertificateAuthorityData: caCert,
				},
			},
		},
		Contexts: []NamedContext{
			{
				Name: clusterName,
				Context: Context{
					Cluster: clusterName,
					User:    clusterName,
				},
			},
		},
		CurrentContext: clusterName,
		Users: []NamedUser{
			{
				Name: clusterName,
				User: User{
					Token: token,
				},
			},
		},
	}
}

// BuildEKSKubeConfigWithExec builds a KubeConfig that uses the AWS CLI exec
// credential plugin to obtain fresh tokens on every API call. The resulting
// kubeconfig contains no embedded token, so it is stable across runs and
// produces no Pulumi state diff on token rotation.
func BuildEKSKubeConfigWithExec(endpoint, caCert, clusterName, region string) KubeConfig {
	return KubeConfig{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: []NamedCluster{
			{
				Name: clusterName,
				Cluster: Cluster{
					Server:                   endpoint,
					CertificateAuthorityData: caCert,
				},
			},
		},
		Contexts: []NamedContext{
			{
				Name: clusterName,
				Context: Context{
					Cluster: clusterName,
					User:    clusterName,
				},
			},
		},
		CurrentContext: clusterName,
		Users: []NamedUser{
			{
				Name: clusterName,
				User: User{
					Exec: &ExecConfig{
						APIVersion:      "client.authentication.k8s.io/v1beta1",
						Command:         "aws",
						Args:            []string{"--region", region, "eks", "get-token", "--cluster-name", clusterName},
						InteractiveMode: "Never",
					},
				},
			},
		},
	}
}

// WriteKubeConfig marshals to YAML and writes to file with 0600 permissions
func WriteKubeConfig(config KubeConfig, filePath string) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal kubeconfig: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write kubeconfig to %s: %w", filePath, err)
	}

	return nil
}

// AddProxyToKubeConfig reads existing kubeconfig, adds proxy-url to all clusters, writes back
func AddProxyToKubeConfig(filePath string, proxyURL string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig: %w", err)
	}

	modified, err := AddProxyToKubeConfigBytes(content, proxyURL)
	if err != nil {
		return err
	}

	if err := os.WriteFile(filePath, modified, 0600); err != nil {
		return fmt.Errorf("failed to write modified kubeconfig: %w", err)
	}

	return nil
}

// AddProxyToKubeConfigBytes injects a proxy-url into every cluster entry
// in the given kubeconfig YAML bytes.
func AddProxyToKubeConfigBytes(data []byte, proxyURL string) ([]byte, error) {
	var config map[string]interface{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig YAML: %w", err)
	}

	if clusters, ok := config["clusters"].([]interface{}); ok {
		for _, cluster := range clusters {
			if clusterMap, ok := cluster.(map[interface{}]interface{}); ok {
				if clusterInfo, ok := clusterMap["cluster"].(map[interface{}]interface{}); ok {
					clusterInfo["proxy-url"] = proxyURL
				}
			}
		}
	}

	modified, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal modified kubeconfig: %w", err)
	}

	return modified, nil
}
