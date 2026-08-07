package azure

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

func StorageAccountExists(ctx context.Context, credentials *Credentials, subscriptionId string, resourceGroupName string, name string) bool {
	clientFactory, err := armstorage.NewClientFactory(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return false
	}
	accountsClient := clientFactory.NewAccountsClient()

	_, err = accountsClient.GetProperties(ctx, resourceGroupName, name, nil)
	if err != nil {
		return false
	}
	return true
}

func CreateStorageAccount(ctx context.Context, credentials *Credentials, subscriptionId string, region string, resourceGroupName string, name string) error {
	clientFactory, err := armstorage.NewClientFactory(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return err
	}
	accountsClient := clientFactory.NewAccountsClient()
	pollerResp, err := accountsClient.BeginCreate(ctx, resourceGroupName, name, armstorage.AccountCreateParameters{
		Location: to.Ptr(region),
		// StorageV2 is the current general-purpose account kind. The legacy
		// BlobStorage kind lacks newer security/network properties that customer
		// security baselines (Azure Policy) evaluate against. StorageV2 is a
		// superset; Pulumi state (block blobs via azblob://) behaves identically.
		Kind: to.Ptr(armstorage.KindStorageV2),
		SKU:  to.Ptr(armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardLRS)}), // so many pointers.
		Properties: &armstorage.AccountPropertiesCreateParameters{
			AllowBlobPublicAccess: to.Ptr(false),
			AccessTier:            to.Ptr(armstorage.AccessTierCool),
		},
	}, nil)
	if err != nil {
		return err
	}

	_, err = pollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}

	return nil
}
func BlobContainerExists(ctx context.Context, credentials *Credentials, subscriptionId string, resourceGroupName string, storageAccountName string, containerName string) bool {
	clientFactory, err := armstorage.NewClientFactory(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return false
	}
	containersClient := clientFactory.NewBlobContainersClient()

	_, err = containersClient.Get(ctx, resourceGroupName, storageAccountName, containerName, nil)
	if err != nil {
		return false
	}
	return true
}

func CreateBlobContainer(ctx context.Context, credentials *Credentials, subscriptionId string, resourceGroupName string, storageAccountName string, containerName string) error {
	clientFactory, err := armstorage.NewClientFactory(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return err
	}
	containersClient := clientFactory.NewBlobContainersClient()
	_, err = containersClient.Create(ctx, resourceGroupName, storageAccountName, containerName, armstorage.BlobContainer{
		Name: to.Ptr(containerName),
	}, nil)
	if err != nil {
		return err
	}

	return nil
}
