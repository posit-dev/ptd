package kube

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/types"
)

// SetupKubeConfig sets up a kubeconfig file for the given target and returns the path.
// It handles:
//   - Determining cloud provider (AWS/Azure)
//   - For AWS: native SDK-based kubeconfig generation (no CLI dependency)
//   - For Azure: native SDK-based kubeconfig generation
//   - Adding proxy configuration for non-tailscale clusters
func SetupKubeConfig(ctx context.Context, t types.Target, creds types.Credentials) (string, error) {
	// Create a temp kubeconfig path
	kubeconfigPath := filepath.Join(os.TempDir(), fmt.Sprintf("kubeconfig-%s", t.HashName()))

	switch t.CloudProvider() {
	case types.AWS:
		return setupAWSKubeConfig(ctx, t, creds, kubeconfigPath)
	case types.Azure:
		return setupAzureKubeConfig(ctx, t, creds, kubeconfigPath)
	default:
		return "", fmt.Errorf("kubeconfig setup not implemented for cloud provider: %s", t.CloudProvider())
	}
}

func setupAWSKubeConfig(ctx context.Context, t types.Target, creds types.Credentials, kubeconfigPath string) (string, error) {
	// Convert to AWS credentials
	awsCreds, err := aws.OnlyAwsCredentials(creds)
	if err != nil {
		return "", fmt.Errorf("failed to get AWS credentials: %w", err)
	}

	// Determine cluster name based on target type
	var clusterName string
	if t.ControlRoom() {
		// Control room: extract environment from target name (last dash), format `main01-{environment}`
		targetName := t.Name()
		if lastDash := strings.LastIndex(targetName, "-"); lastDash != -1 {
			environment := targetName[lastDash+1:]
			clusterName = fmt.Sprintf("main01-%s", environment)
		} else {
			// Fallback if no dash found
			clusterName = fmt.Sprintf("main01-%s", targetName)
		}
	} else {
		// Workload: type-assert to aws.Target, get first key from Clusters map
		awsTarget, ok := t.(aws.Target)
		if !ok {
			return "", fmt.Errorf("failed to type-assert to aws.Target")
		}

		targetName := t.Name()
		slog.Debug("Workload target cluster info", "target", targetName, "clusters_count", len(awsTarget.Clusters))

		if len(awsTarget.Clusters) > 0 {
			// Get the first cluster key and format as {targetName}-{releaseKey}
			for releaseKey := range awsTarget.Clusters {
				slog.Debug("Found cluster release", "release_key", releaseKey)
				clusterName = fmt.Sprintf("%s-%s", targetName, releaseKey)
				break
			}
		}

		// If no cluster name found, use fallback
		if clusterName == "" {
			slog.Debug("No cluster name found, using fallback")
			clusterName = fmt.Sprintf("%s-main", targetName)
		}
	}

	slog.Info("Setting up kubeconfig", "cluster_name", clusterName, "region", t.Region(), "target", t.Name())

	// Get cluster info from AWS
	endpoint, caCert, err := aws.GetClusterInfo(ctx, awsCreds, t.Region(), clusterName)
	if err != nil {
		return "", fmt.Errorf("failed to get cluster info: %w", err)
	}

	// Get EKS token
	token, err := aws.GetEKSToken(ctx, awsCreds, t.Region(), clusterName)
	if err != nil {
		return "", fmt.Errorf("failed to get EKS token: %w", err)
	}

	// Build kubeconfig
	config := BuildEKSKubeConfig(endpoint, caCert, token, clusterName)

	// Write kubeconfig
	if err := WriteKubeConfig(config, kubeconfigPath); err != nil {
		return "", fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	// Add proxy if not using tailscale
	if !t.TailscaleEnabled() {
		if err := AddProxyToKubeConfig(kubeconfigPath, "socks5://localhost:1080"); err != nil {
			return "", fmt.Errorf("failed to add proxy to kubeconfig: %w", err)
		}
	}

	return kubeconfigPath, nil
}

func setupAzureKubeConfig(ctx context.Context, t types.Target, creds types.Credentials, kubeconfigPath string) (string, error) {
	// Type-assert to azure.Target
	azureTarget, ok := t.(azure.Target)
	if !ok {
		return "", fmt.Errorf("failed to type-assert to azure.Target")
	}

	// Get first cluster key from Clusters map
	var clusterName string
	targetName := t.Name()

	if clusters := azureTarget.Clusters(); len(clusters) > 0 {
		// Format as {targetName}-{releaseKey}
		for releaseKey := range clusters {
			clusterName = fmt.Sprintf("%s-%s", targetName, releaseKey)
			break
		}
	}

	if clusterName == "" {
		return "", fmt.Errorf("no clusters found for Azure target %s", targetName)
	}

	// Get resource group
	resourceGroup := azureTarget.ResourceGroupName()

	// Convert to Azure credentials
	azureCreds, err := azure.OnlyAzureCredentials(creds)
	if err != nil {
		return "", fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	slog.Info("Setting up Azure kubeconfig",
		"cluster_name", clusterName,
		"resource_group", resourceGroup,
		"subscription", azureTarget.SubscriptionID(),
		"target", targetName)

	// Get kubeconfig from Azure
	kubeconfigBytes, err := azure.GetKubeCredentials(ctx, azureCreds, azureTarget.SubscriptionID(), resourceGroup, clusterName)
	if err != nil {
		return "", fmt.Errorf("failed to get Azure kubeconfig: %w", err)
	}

	// Write the raw bytes to file
	if err := os.WriteFile(kubeconfigPath, kubeconfigBytes, 0600); err != nil {
		return "", fmt.Errorf("failed to write Azure kubeconfig: %w", err)
	}

	// Always add proxy for Azure (no tailscale support)
	if err := AddProxyToKubeConfig(kubeconfigPath, "socks5://localhost:1080"); err != nil {
		return "", fmt.Errorf("failed to add proxy to Azure kubeconfig: %w", err)
	}

	return kubeconfigPath, nil
}