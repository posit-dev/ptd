package azure

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/google/uuid"
)

func ResourceGroupExists(ctx context.Context, credentials *Credentials, subscriptionId string, region string, name string) bool {
	rgClient, err := armresources.NewResourceGroupsClient(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return false
	}

	_, err = rgClient.Get(ctx, name, nil)
	if err != nil {
		return false
	}
	return true
}

func CreateResourceGroup(ctx context.Context, credentials *Credentials, subscriptionId string, region string, name string) error {
	rgClient, err := armresources.NewResourceGroupsClient(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return err
	}

	param := armresources.ResourceGroup{
		Location: to.Ptr(region),
	}

	_, err = rgClient.CreateOrUpdate(ctx, name, param, nil)
	if err != nil {
		return err
	}

	return nil
}

func RoleAssignmentExists(ctx context.Context, credentials *Credentials, subscriptionId string, resourceGroupName string, principalGroupID string, roleID string) (bool, error) {
	roleScope := "/subscriptions/" + subscriptionId + "/resourceGroups/" + resourceGroupName
	roleDefinitionID := "/subscriptions/" + subscriptionId + "/providers/Microsoft.Authorization/roleDefinitions/" + roleID

	authClientFactory, err := armauthorization.NewClientFactory(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return false, err
	}
	roleAssignmentsClient := authClientFactory.NewRoleAssignmentsClient()

	pager := roleAssignmentsClient.NewListForScopePager(roleScope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: to.Ptr("atScope()"),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, err
		}
		for _, assignment := range page.Value {
			if assignment.Properties != nil &&
				assignment.Properties.PrincipalID != nil &&
				assignment.Properties.RoleDefinitionID != nil &&
				*assignment.Properties.PrincipalID == principalGroupID &&
				*assignment.Properties.RoleDefinitionID == roleDefinitionID {
				return true, nil
			}
		}
	}
	return false, nil
}

func CreateRoleAssignment(ctx context.Context, credentials *Credentials, subscriptionId string, resourceGroupName string, principalGroupID string, roleID string) error {
	roleScope := "/subscriptions/" + subscriptionId + "/resourceGroups/" + resourceGroupName
	roleDefinitionID := "/subscriptions/" + subscriptionId + "/providers/Microsoft.Authorization/roleDefinitions/" + roleID

	authClientFactory, err := armauthorization.NewClientFactory(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return err
	}
	roleAssignmentsClient := authClientFactory.NewRoleAssignmentsClient()

	assignmentID := uuid.NewString()
	_, err = roleAssignmentsClient.Create(
		ctx,
		roleScope,
		assignmentID,
		armauthorization.RoleAssignmentCreateParameters{
			Properties: &armauthorization.RoleAssignmentProperties{
				PrincipalID:      to.Ptr(principalGroupID),
				RoleDefinitionID: to.Ptr(roleDefinitionID),
				PrincipalType:    to.Ptr(armauthorization.PrincipalTypeGroup),
			},
		},
		nil,
	)
	if err != nil {
		return err
	}

	return nil
}
