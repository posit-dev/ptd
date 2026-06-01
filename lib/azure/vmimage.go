package azure

import (
	"context"
	"fmt"
	"sort"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

// GetLatestVMImageVersion returns the latest VM image version for a given
// publisher / offer / sku in a location. It mirrors
// ptd.azure_sdk.get_latest_vm_image_version (used by the persistent bastion
// jumpbox). Azure version strings sort lexicographically, so the lexicographic
// max is the latest version.
func GetLatestVMImageVersion(ctx context.Context, creds *Credentials, subscriptionID, location, publisher, offer, sku string) (string, error) {
	clientFactory, err := armcompute.NewClientFactory(subscriptionID, creds.AzureCredential(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create compute client factory: %w", err)
	}

	client := clientFactory.NewVirtualMachineImagesClient()
	result, err := client.List(ctx, location, publisher, offer, sku, nil)
	if err != nil {
		return "", fmt.Errorf("failed to list VM image versions for %s/%s/%s in %s: %w", publisher, offer, sku, location, err)
	}

	versions := make([]string, 0, len(result.VirtualMachineImageResourceArray))
	for _, img := range result.VirtualMachineImageResourceArray {
		if img != nil && img.Name != nil {
			versions = append(versions, *img.Name)
		}
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no images found for %s/%s/%s in %s", publisher, offer, sku, location)
	}

	sort.Strings(versions)
	return versions[len(versions)-1], nil
}
