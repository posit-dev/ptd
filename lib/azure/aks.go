package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

// ClusterIdentityInfo holds the principal IDs, OIDC URL, and VNet info fetched from a live AKS cluster.
type ClusterIdentityInfo struct {
	// ClusterPrincipalID is the system-assigned identity principal ID of the AKS control plane.
	ClusterPrincipalID string
	// KubeletPrincipalID is the kubelet identity object ID (used for ACR pull).
	KubeletPrincipalID string
	// OIDCIssuerURL is the cluster's OIDC issuer URL (used for federated identity credentials).
	OIDCIssuerURL string
	// VNetSubnetID is the subnet resource ID of the first agent pool (used for bastion NSG lookup).
	// Format: /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/virtualNetworks/{vnet}/subnets/{subnet}
	VNetSubnetID string
}

// GetClusterIdentityInfo retrieves identity information from a live AKS cluster.
func GetClusterIdentityInfo(ctx context.Context, creds *Credentials, subscriptionID, resourceGroup, clusterName string) (*ClusterIdentityInfo, error) {
	clientFactory, err := armcontainerservice.NewClientFactory(subscriptionID, creds.AzureCredential(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create AKS client factory: %w", err)
	}

	client := clientFactory.NewManagedClustersClient()
	result, err := client.Get(ctx, resourceGroup, clusterName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get AKS cluster %s: %w", clusterName, err)
	}

	info := &ClusterIdentityInfo{}

	// Cluster system-assigned identity (used for reader + network-contributor roles).
	if result.Identity != nil && result.Identity.PrincipalID != nil {
		info.ClusterPrincipalID = *result.Identity.PrincipalID
	}

	// Kubelet identity (used for ACR pull and Azure Files CSI roles).
	if result.Properties != nil && result.Properties.IdentityProfile != nil {
		if kubelet, ok := result.Properties.IdentityProfile["kubeletidentity"]; ok && kubelet != nil && kubelet.ObjectID != nil {
			info.KubeletPrincipalID = *kubelet.ObjectID
		}
	}

	// OIDC issuer URL (used for federated identity credentials).
	if result.Properties != nil && result.Properties.OidcIssuerProfile != nil && result.Properties.OidcIssuerProfile.IssuerURL != nil {
		info.OIDCIssuerURL = *result.Properties.OidcIssuerProfile.IssuerURL
	}

	// VNet subnet ID from the first agent pool profile (used for bastion NSG).
	if result.Properties != nil && len(result.Properties.AgentPoolProfiles) > 0 {
		if result.Properties.AgentPoolProfiles[0].VnetSubnetID != nil {
			info.VNetSubnetID = *result.Properties.AgentPoolProfiles[0].VnetSubnetID
		}
	}

	return info, nil
}

// GetKubeCredentials retrieves the kubeconfig for an AKS cluster using the Azure SDK.
func GetKubeCredentials(ctx context.Context, creds *Credentials, subscriptionID, resourceGroup, clusterName string) ([]byte, error) {
	clientFactory, err := armcontainerservice.NewClientFactory(subscriptionID, creds.AzureCredential(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create AKS client factory: %w", err)
	}

	client := clientFactory.NewManagedClustersClient()
	result, err := client.ListClusterUserCredentials(ctx, resourceGroup, clusterName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster credentials for %s: %w", clusterName, err)
	}

	if len(result.Kubeconfigs) == 0 || result.Kubeconfigs[0].Value == nil {
		return nil, fmt.Errorf("no kubeconfig returned for cluster %s", clusterName)
	}

	return result.Kubeconfigs[0].Value, nil
}
