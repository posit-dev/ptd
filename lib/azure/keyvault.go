package azure

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
)

// Key Vault Administrator built-in role, this won't change
// const keyVaultAdminRoleId = "00482a5a-887f-4fb3-b363-3b7fe8e74483"

func KeyVaultExists(ctx context.Context, credentials *Credentials, subscriptionId string, resourceGroupName string, name string) bool {
	clientFactory, err := armkeyvault.NewClientFactory(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return false
	}
	vaultsClient := clientFactory.NewVaultsClient()

	_, err = vaultsClient.Get(ctx, resourceGroupName, name, nil)
	if err != nil {
		return false
	}
	return true
}

func CreateKeyVault(ctx context.Context, credentials *Credentials, subscriptionId string, tenantId string, region string, resourceGroupName string, name string) error {
	clientFactory, err := armkeyvault.NewClientFactory(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return err
	}
	vaultsClient := clientFactory.NewVaultsClient()
	pollerResp, err := vaultsClient.BeginCreateOrUpdate(
		ctx,
		resourceGroupName,
		name,
		armkeyvault.VaultCreateOrUpdateParameters{
			Location: to.Ptr(region),
			Properties: &armkeyvault.VaultProperties{
				SKU: &armkeyvault.SKU{
					Family: to.Ptr(armkeyvault.SKUFamilyA),
					Name:   to.Ptr(armkeyvault.SKUNameStandard),
				},
				TenantID:                to.Ptr(tenantId),
				EnabledForDeployment:    to.Ptr(true),
				CreateMode:              to.Ptr(armkeyvault.CreateModeDefault),
				EnableRbacAuthorization: to.Ptr(true),
				PublicNetworkAccess:     to.Ptr("enabled"),
			},
		},
		nil,
	)
	if err != nil {
		return err
	}

	_, err = pollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}

	return nil
}

func KeyExists(ctx context.Context, credentials *Credentials, subscriptionId string, resourceGroupName string, vaultName string, keyName string) bool {
	clientFactory, err := armkeyvault.NewClientFactory(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return false
	}
	keysClient := clientFactory.NewKeysClient()

	_, err = keysClient.Get(ctx, resourceGroupName, vaultName, keyName, nil)
	if err != nil {
		return false
	}
	return true
}

func CreateKey(ctx context.Context, credentials *Credentials, subscriptionId string, resourceGroupName string, vaultName string, keyName string) error {
	clientFactory, err := armkeyvault.NewClientFactory(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return err
	}
	keysClient := clientFactory.NewKeysClient()

	_, err = keysClient.CreateIfNotExist(ctx, resourceGroupName, vaultName, keyName, armkeyvault.KeyCreateParameters{
		Properties: &armkeyvault.KeyProperties{
			Kty: to.Ptr(armkeyvault.JSONWebKeyTypeRSA),
		},
	}, nil)

	return err
}
