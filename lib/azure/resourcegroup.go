package azure

import (
	"context"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/google/uuid"
)

// FormatTagKey converts a PTD tag key to a valid Azure tag key. Azure tag keys
// cannot contain '/', so we replace '/' with ':' (e.g. posit.team/environment ->
// posit.team:environment). It is the single source of truth for the key-format
// rule shared by the persistent step (azureTagMap) and resource-group creation,
// so tags applied at RG creation match the tags placed on child resources and do
// not churn.
func FormatTagKey(key string) string {
	return strings.ReplaceAll(key, "/", ":")
}

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

// CreateResourceGroup creates (or updates) the resource group, applying tags at
// creation time. Tag keys are run through FormatTagKey so they match the keys
// placed on child resources by the persistent step (azureTagMap), preventing tag
// churn.
//
// This wraps ARM's CreateOrUpdate, which is idempotent but WILL overwrite the tags
// of an already-existing resource group. Callers that must not retag an existing
// RG (e.g. to leave manually backfilled tags intact) must guard the call with
// ResourceGroupExists, as bootstrap does. Consequently a change to a workload's
// resource_tags is applied only to newly created RGs, never retroactively to
// existing ones.
func CreateResourceGroup(ctx context.Context, credentials *Credentials, subscriptionId string, region string, name string, tags map[string]string) error {
	rgClient, err := armresources.NewResourceGroupsClient(subscriptionId, credentials.credentials, nil)
	if err != nil {
		return err
	}

	param := armresources.ResourceGroup{
		Location: to.Ptr(region),
	}
	if len(tags) > 0 {
		azureTags := make(map[string]*string, len(tags))
		for k, v := range tags {
			azureTags[FormatTagKey(k)] = to.Ptr(v)
		}
		param.Tags = azureTags
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
