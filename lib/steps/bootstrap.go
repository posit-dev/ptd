package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/consts"
	"github.com/posit-dev/ptd/lib/secrets"
	"github.com/posit-dev/ptd/lib/types"
)

type BootstrapStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
	Log       *slog.Logger
}

func (s *BootstrapStep) Name() string {
	return "bootstrap"
}

func (s *BootstrapStep) ProxyRequired() bool {
	return false
}

func (s *BootstrapStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
	s.Log = setLoggerWithContext(t, controlRoomTarget, options, "bootstrap")
}

func (s *BootstrapStep) Run(ctx context.Context) error {
	// get the credentials for the target
	creds, err := s.DstTarget.Credentials(ctx)
	if err != nil {
		return err
	}

	switch s.DstTarget.CloudProvider() {
	case types.AWS:
		err = s.runAws(ctx, creds, s.DstTarget.Name())
		if err != nil {
			return err
		}
	case types.Azure:
		err = s.runAzure(ctx, creds, s.DstTarget.Name())
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported cloud provider: %s", s.DstTarget.CloudProvider())
	}

	return nil
}

func (s *BootstrapStep) runAws(ctx context.Context, creds types.Credentials, workloadName string) error {
	awsCreds, err := aws.OnlyAwsCredentials(creds)
	if err != nil {
		return err
	}

	// ensure pulumi state bucket
	s.Log.Info("Creating bucket for pulumi state if it doesn't exist", "bucket", s.DstTarget.StateBucketName())
	if !aws.BucketExists(ctx, awsCreds, s.DstTarget.Region(), s.DstTarget.StateBucketName()) {
		err = aws.CreateBucket(ctx, awsCreds, s.DstTarget.Region(), s.DstTarget.StateBucketName())
		if err != nil {
			return err
		}
	}

	// ensure pulumi state kms key exists
	s.Log.Info("Creating KMS key for pulumi state if it doesn't exist", "alias", consts.KmsAlias)
	if !aws.KmsKeyExists(ctx, awsCreds, s.DstTarget.Region(), consts.KmsAlias) {
		_, err = aws.CreateKmsKey(ctx, awsCreds, s.DstTarget.Region(), consts.KmsAlias, "")
		if err != nil {
			return err
		}
	}

	// ensure admin policy exists (required for permissions boundary on IAM roles)
	// This is particularly important for workloads using custom_role where the
	// standard admin setup didn't create the policy.
	// Only create if the feature flag is enabled.
	awsTarget, ok := s.DstTarget.(aws.Target)
	if ok && awsTarget.CreateAdminPolicyAsResource() {
		s.Log.Info("Creating admin policy if it doesn't exist", "policyName", consts.PositTeamDedicatedAdminPolicyName)
		err = aws.CreateAdminPolicyIfNotExists(ctx, awsCreds, s.DstTarget.Region(), awsCreds.AccountID(), consts.PositTeamDedicatedAdminPolicyName)
		if err != nil {
			return fmt.Errorf("failed to ensure admin policy exists: %w", err)
		}
	}

	// create an empty workload secret
	var workloadSecret secrets.AWSWorkloadSecret
	s.Log.Info("Creating workload secret if it doesn't exist")
	if !s.Options.DryRun {
		workloadSecretName := fmt.Sprintf("%s.posit.team", workloadName)
		err = s.DstTarget.SecretStore().CreateSecretIfNotExists(ctx, creds, workloadSecretName, workloadSecret)
		if err != nil {
			return err
		}
	}

	// TODO: ctrl room bootstrap ensured a "vault" secret (<siteName>.ctrl.posit.team)
	// it currently contains a mimir salt and opsgenie key
	// control room creation may not even be a thing we do again.

	// create a (partially empty) site secret and site session secret for each site
	// this will be filled in more(?) by later steps.
	for siteName := range s.DstTarget.Sites() {
		siteSecretName := fmt.Sprintf("%s-%s.posit.team", s.DstTarget.Name(), siteName)
		s.Log.Info("Creating site secret if it doesn't exist", "site", siteName, "secretName", siteSecretName)
		if !s.Options.DryRun {
			err = s.DstTarget.SecretStore().CreateSecretIfNotExists(ctx, creds, siteSecretName, secrets.NewSiteSecret(siteSecretName))
			if err != nil {
				return err
			}
		}

		s.Log.Info("Creating site session secret if it doesn't exist", "site", siteName)
		siteSessionSecretName := fmt.Sprintf("%s-%s.sessions.posit.team", s.DstTarget.Name(), siteName)
		if !s.Options.DryRun {
			err = s.DstTarget.SecretStore().CreateSecretIfNotExists(ctx, creds, siteSessionSecretName, secrets.SiteSessionSecret{})
			if err != nil {
				return err
			}
		}

		// Create SSH vault for Package Manager if it doesn't exist
		sshVaultName := fmt.Sprintf("%s-%s-ssh-ppm-keys.posit.team", s.DstTarget.Name(), siteName)
		s.Log.Info("Creating SSH vault for Package Manager if it doesn't exist", "site", siteName, "vaultName", sshVaultName)
		if !s.Options.DryRun {
			err = s.DstTarget.SecretStore().CreateSecretIfNotExists(ctx, creds, sshVaultName, secrets.SSHVaultSecret{})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *BootstrapStep) runAzure(ctx context.Context, c types.Credentials, _ string) error {
	azureCreds, err := azure.OnlyAzureCredentials(c)
	if err != nil {
		return err
	}

	azureTarget := s.DstTarget.(azure.Target)

	// ensure pulumi state resource group
	s.Log.Info("Creating resource group for pulumi state if it doesn't exist", "resourceGroup", azureTarget.ResourceGroupName())
	exists := azure.ResourceGroupExists(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.Region(), azureTarget.ResourceGroupName())
	if !exists {
		err := azure.CreateResourceGroup(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.Region(), azureTarget.ResourceGroupName())
		if err != nil {
			return err
		}
	}

	s.Log.Info("Creating key vault for pulumi state if it doesn't exist", "vaultName", azureTarget.VaultName())
	if !azure.KeyVaultExists(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.VaultName()) {
		err = azure.CreateKeyVault(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.TenantID(), azureTarget.Region(), azureTarget.ResourceGroupName(), azureTarget.VaultName())
		if err != nil {
			return err
		}
		err = azure.CreateRoleAssignment(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.AdminGroupID(), consts.KeyVaultAdminRoleId)
		if err != nil {
			return err
		}
	} else {
		exists, err := azure.RoleAssignmentExists(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.AdminGroupID(), consts.KeyVaultAdminRoleId)
		if err != nil {
			return err
		}
		if !exists {
			err = azure.CreateRoleAssignment(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.AdminGroupID(), consts.KeyVaultAdminRoleId)
			if err != nil {
				return err
			}
		}
	}

	s.Log.Info("Creating key for pulumi state if it doesn't exist", "keyName", consts.AzKeyName)
	if !azure.KeyExists(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.VaultName(), consts.AzKeyName) {
		err = azure.CreateKey(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.VaultName(), consts.AzKeyName)
		if err != nil {
			return err
		}
	}

	s.Log.Info("Creating storage account for pulumi state if it doesn't exist", "storageAccountName", azureTarget.StateBucketName())
	if !azure.StorageAccountExists(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.StateBucketName()) {
		err = azure.CreateStorageAccount(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.Region(), azureTarget.ResourceGroupName(), azureTarget.StateBucketName())
		if err != nil {
			return err
		}
		err = azure.CreateRoleAssignment(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.AdminGroupID(), consts.StorageBlobDataContribRoleId)
		if err != nil {
			return err
		}
	} else {
		exists, err := azure.RoleAssignmentExists(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.AdminGroupID(), consts.StorageBlobDataContribRoleId)
		if err != nil {
			return err
		}
		if !exists {
			err = azure.CreateRoleAssignment(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.AdminGroupID(), consts.StorageBlobDataContribRoleId)
			if err != nil {
				return err
			}
		}
	}

	s.Log.Info("Creating blob container for pulumi state if it doesn't exist", "containerName", azureTarget.BlobStorageName())
	if !azure.BlobContainerExists(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.StateBucketName(), azureTarget.BlobStorageName()) {
		err = azure.CreateBlobContainer(ctx, azureCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), azureTarget.StateBucketName(), azureTarget.BlobStorageName())
		if err != nil {
			return err
		}
	}

	// create site secrets, certain site secret values are populated in later steps rather than here
	for siteName := range s.DstTarget.Sites() {
		s.Log.Info("Creating site secrets if they don't exist", "site", siteName)

		siteSecret := secrets.NewSiteSecret(siteName)
		jsonData, err := json.Marshal(siteSecret)
		if err != nil {
			return fmt.Errorf("failed to marshal site secret: %w", err)
		}

		var secretMap map[string]any
		if err := json.Unmarshal(jsonData, &secretMap); err != nil {
			return fmt.Errorf("failed to unmarshal secret: %w", err)
		}

		// Create a KeyVault secret for each populated field in the map since Azure Secret Provider
		// doesn't support json blobs, we must create a KV entry for each field.
		for fieldName, fieldValue := range secretMap {
			fieldSecretName := fmt.Sprintf("%s-%s", siteName, fieldName)
			fieldValueStr := fmt.Sprintf("%v", fieldValue)
			if fieldValueStr == "" {
				continue
			}
			s.Log.Info("Creating site secret if it doesn't exist", "secretName", fieldSecretName)
			if !s.Options.DryRun {
				if err := s.DstTarget.SecretStore().CreateSecretIfNotExists(ctx, azureCreds, fieldSecretName, fieldValueStr); err != nil {
					return fmt.Errorf("failed to create secret for field %s: %w", fieldName, err)
				}
			}
		}

		// create session secrets here if we ever need to bootstrap them, currently not all sites use session secrets

	}

	return nil
}
