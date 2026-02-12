package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

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