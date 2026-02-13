package steps

import (
	"context"
	"errors"
	"fmt"

	"github.com/pulumi/pulumi-azure-native-sdk/containerservice/v3"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/helpers"
	ptdpulumi "github.com/posit-dev/ptd/lib/pulumi"
	"github.com/posit-dev/ptd/lib/types"
)

type AKSStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *AKSStep) Name() string {
	return "aks"
}

func (s *AKSStep) ProxyRequired() bool {
	return false
}

func (s *AKSStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *AKSStep) Run(ctx context.Context) error {
	if s.DstTarget == nil {
		return errors.New("AKS step requires a destination target")
	}

	creds, err := s.DstTarget.Credentials(ctx)
	if err != nil {
		return err
	}

	envVars, err := prepareEnvVarsForPulumi(ctx, s.DstTarget, creds)
	if err != nil {
		return err
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, s.deploy, envVars)
	if err != nil {
		return err
	}

	err = runPulumi(ctx, stack, s.Options)
	if err != nil {
		return err
	}

	return nil
}

func (s *AKSStep) deploy(ctx *pulumi.Context, target types.Target) error {
	c, err := helpers.ConfigForTarget(target)
	if err != nil {
		return err
	}

	config, ok := c.(types.AzureWorkloadConfig)
	if !ok {
		return errors.New("expected AzureWorkloadConfig for AKS step")
	}

	azTarget, ok := target.(azure.Target)
	if !ok {
		return errors.New("expected Azure target for AKS step")
	}

	persistentOutputs, err := getPersistentStackOutputs(ctx.Context(), target)
	if err != nil {
		return err
	}

	if _, ok := persistentOutputs["vnet_name"]; !ok {
		return fmt.Errorf("vnet_name output not found in persistent stack outputs")
	}

	vnetName := persistentOutputs["vnet_name"].Value.(string)

	if _, ok := persistentOutputs["private_subnet_name"]; !ok {
		return fmt.Errorf("private_subnet_name output not found in persistent stack outputs")
	}

	privateSubnetName := persistentOutputs["private_subnet_name"].Value.(string)

	vnetRsgName := azTarget.ResourceGroupName()
	if config.Network.VnetRsgName != "" {
		vnetRsgName = config.Network.VnetRsgName
	}
	subnetId := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s", config.SubscriptionID, vnetRsgName, vnetName, privateSubnetName)

	for release, clusterConfig := range config.Clusters {
		// Validate and resolve user node pools from configuration
		userNodePools, err := clusterConfig.ResolveUserNodePools()
		if err != nil {
			return err
		}

		// Validate user node pools
		if err := azure.ValidateUserNodePools(userNodePools); err != nil {
			return fmt.Errorf("invalid user node pool configuration: %w", err)
		}

		// Validate outbound type
		if err := clusterConfig.ValidateOutboundType(); err != nil {
			return fmt.Errorf("invalid outbound type configuration: %w", err)
		}

		// Determine system node pool disk size (smaller than user pools since it only runs system components)
		systemNodePoolDiskSize := 128 // Default for system node pool
		if clusterConfig.SystemNodePoolRootDiskSize != nil {
			systemNodePoolDiskSize = *clusterConfig.SystemNodePoolRootDiskSize
		}

		agentPoolProfiles := containerservice.ManagedClusterAgentPoolProfileArray{
			&containerservice.ManagedClusterAgentPoolProfileArgs{ // System Node Pool
				AvailabilityZones: pulumi.StringArray{
					pulumi.String("2"),
					pulumi.String("3"),
				},
				Count:              pulumi.Int(2),
				EnableAutoScaling:  pulumi.Bool(true),
				EnableFIPS:         pulumi.Bool(false),
				EnableNodePublicIP: pulumi.Bool(false),
				KubeletDiskType:    pulumi.String(containerservice.KubeletDiskTypeOS),
				MaxCount:           pulumi.Int(5),
				MaxPods:            pulumi.Int(110),
				MinCount:           pulumi.Int(2),
				Mode:               pulumi.String(containerservice.AgentPoolModeSystem),
				Name:               pulumi.String("agentpool"),
				NodeTaints: pulumi.StringArray{
					pulumi.String("CriticalAddonsOnly=true:NoSchedule"),
				},
				OrchestratorVersion: pulumi.String(clusterConfig.KubernetesVersion),
				OsDiskSizeGB:        pulumi.Int(systemNodePoolDiskSize),
				OsDiskType:          pulumi.String(containerservice.OSDiskTypeManaged),
				OsSKU:               pulumi.String(containerservice.OSSKUUbuntu),
				OsType:              pulumi.String(containerservice.OSTypeLinux),
				ScaleDownMode:       pulumi.String(containerservice.ScaleDownModeDelete),
				SecurityProfile: &containerservice.AgentPoolSecurityProfileArgs{
					EnableSecureBoot: pulumi.Bool(false),
					EnableVTPM:       pulumi.Bool(false),
				},
				Tags: pulumi.StringMap{
					"Owner": pulumi.String("ptd"),
				},
				Type: pulumi.String(containerservice.AgentPoolTypeVirtualMachineScaleSets),
				UpgradeSettings: &containerservice.AgentPoolUpgradeSettingsArgs{
					MaxSurge: pulumi.String("10%"),
				},
				VmSize:       pulumi.String(clusterConfig.SystemNodePoolInstanceType),
				VnetSubnetID: pulumi.String(subnetId),
			},
		}

		// For legacy clusters, add the hardcoded "userpool" to AgentPoolProfiles
		useLegacyUserPool := clusterConfig.UseLegacyUserPool != nil && *clusterConfig.UseLegacyUserPool
		if useLegacyUserPool {
			agentPoolProfiles = append(agentPoolProfiles, &containerservice.ManagedClusterAgentPoolProfileArgs{
				AvailabilityZones: pulumi.StringArray{
					pulumi.String("2"),
					pulumi.String("3"),
				},
				Count:               pulumi.Int(4),
				EnableAutoScaling:   pulumi.Bool(true),
				EnableFIPS:          pulumi.Bool(false),
				EnableNodePublicIP:  pulumi.Bool(false),
				KubeletDiskType:     pulumi.String(containerservice.KubeletDiskTypeOS),
				MaxCount:            pulumi.Int(10),
				MaxPods:             pulumi.Int(110),
				MinCount:            pulumi.Int(0),
				Mode:                pulumi.String(containerservice.AgentPoolModeUser),
				Name:                pulumi.String("userpool"),
				OrchestratorVersion: pulumi.String(clusterConfig.KubernetesVersion),
				OsDiskSizeGB:        pulumi.Int(128),
				OsDiskType:          pulumi.String(containerservice.OSDiskTypeManaged),
				OsSKU:               pulumi.String(containerservice.OSSKUUbuntu),
				OsType:              pulumi.String(containerservice.OSTypeLinux),
				ScaleDownMode:       pulumi.String(containerservice.ScaleDownModeDelete),
				SecurityProfile: &containerservice.AgentPoolSecurityProfileArgs{
					EnableSecureBoot: pulumi.Bool(false),
					EnableVTPM:       pulumi.Bool(false),
				},
				Tags: pulumi.StringMap{
					"Owner": pulumi.String("ptd"),
				},
				Type: pulumi.String(containerservice.AgentPoolTypeVirtualMachineScaleSets),
				UpgradeSettings: &containerservice.AgentPoolUpgradeSettingsArgs{
					MaxSurge: pulumi.String("10%"),
				},
				VmSize:       pulumi.String(clusterConfig.UserNodePoolInstanceType),
				VnetSubnetID: pulumi.String(subnetId),
			})
		}

		aksCluster, err := containerservice.NewManagedCluster(ctx, fmt.Sprintf("aksCluster-%s", release), &containerservice.ManagedClusterArgs{
			AadProfile: &containerservice.ManagedClusterAADProfileArgs{
				EnableAzureRBAC: pulumi.Bool(true),
				Managed:         pulumi.Bool(true),
				TenantID:        pulumi.String(config.TenantID),
			},
			AddonProfiles: containerservice.ManagedClusterAddonProfileMap{
				"azureKeyvaultSecretsProvider": &containerservice.ManagedClusterAddonProfileArgs{
					Config: pulumi.StringMap{
						"enableSecretRotation": pulumi.String("true"),
						"rotationPollInterval": pulumi.String("2m"),
					},
					Enabled: pulumi.Bool(true),
				},
				"azurepolicy": &containerservice.ManagedClusterAddonProfileArgs{
					Enabled: pulumi.Bool(false),
				},
			},
			AgentPoolProfiles: agentPoolProfiles,
			ApiServerAccessProfile: &containerservice.ManagedClusterAPIServerAccessProfileArgs{
				EnablePrivateCluster:           pulumi.Bool(true),
				EnablePrivateClusterPublicFQDN: pulumi.Bool(true),
				PrivateDNSZone:                 pulumi.String("system"),
			},
			AutoUpgradeProfile: &containerservice.ManagedClusterAutoUpgradeProfileArgs{
				NodeOSUpgradeChannel: pulumi.String(containerservice.NodeOSUpgradeChannelNodeImage),
				UpgradeChannel:       pulumi.String(containerservice.UpgradeChannelPatch),
			},
			DisableLocalAccounts: pulumi.Bool(true),
			// DiskEncryptionSetID: pulumi.String("disk-encryption-set-id"), TODO: enable disk encryption
			DnsPrefix:  pulumi.String(fmt.Sprintf("%s-%s-dns", target.Name(), release)),
			EnableRBAC: pulumi.Bool(true),
			Identity: &containerservice.ManagedClusterIdentityArgs{
				Type: containerservice.ResourceIdentityTypeSystemAssigned,
			},
			KubernetesVersion: pulumi.String(clusterConfig.KubernetesVersion),
			Location:          pulumi.String(target.Region()),
			NetworkProfile: &containerservice.ContainerServiceNetworkProfileArgs{
				IpFamilies: pulumi.StringArray{
					pulumi.String(containerservice.IpFamilyIPv4),
				},
				LoadBalancerSku:   pulumi.String("Standard"),
				NetworkDataplane:  pulumi.String(containerservice.NetworkDataplaneAzure),
				NetworkPlugin:     pulumi.String(containerservice.NetworkPluginAzure),
				NetworkPluginMode: pulumi.String(containerservice.NetworkPluginModeOverlay),
				NetworkPolicy:     pulumi.String(containerservice.NetworkPolicyCalico),
				OutboundType:      pulumi.String(getOutboundType(clusterConfig.OutboundType)),
			},
			OidcIssuerProfile: &containerservice.ManagedClusterOIDCIssuerProfileArgs{
				Enabled: pulumi.Bool(true),
			},
			ResourceGroupName: pulumi.String(azTarget.ResourceGroupName()),
			ResourceName:      pulumi.String(fmt.Sprintf("%s-%s", target.Name(), release)),
			SecurityProfile: &containerservice.ManagedClusterSecurityProfileArgs{
				ImageCleaner: &containerservice.ManagedClusterSecurityProfileImageCleanerArgs{
					Enabled:       pulumi.Bool(true),
					IntervalHours: pulumi.Int(168),
				},
				WorkloadIdentity: &containerservice.ManagedClusterSecurityProfileWorkloadIdentityArgs{
					Enabled: pulumi.Bool(true),
				},
			},
			ServicePrincipalProfile: &containerservice.ManagedClusterServicePrincipalProfileArgs{
				ClientId: pulumi.String("msi"),
			},
			Sku: &containerservice.ManagedClusterSKUArgs{
				Name: pulumi.String(containerservice.ManagedClusterSKUNameBase),
				Tier: pulumi.String(containerservice.ManagedClusterSKUTierStandard),
			},
			StorageProfile: &containerservice.ManagedClusterStorageProfileArgs{
				DiskCSIDriver: &containerservice.ManagedClusterStorageProfileDiskCSIDriverArgs{
					Enabled: pulumi.Bool(true),
				},
				FileCSIDriver: &containerservice.ManagedClusterStorageProfileFileCSIDriverArgs{
					Enabled: pulumi.Bool(true),
				},
				SnapshotController: &containerservice.ManagedClusterStorageProfileSnapshotControllerArgs{
					Enabled: pulumi.Bool(true),
				},
			},
			SupportPlan: pulumi.String("KubernetesOfficial"),
			Tags: pulumi.StringMap{
				"Owner": pulumi.String("ptd"),
			},
		}, pulumi.Protect(config.ProtectPersistentResources),
			pulumi.IgnoreChanges([]string{
				// ignored fields when importing existing clusters to avoid altering fields that we're not (yet) managing in Pulumi
				"autoScalerProfile",
				"identityProfile",
				"networkProfile.loadBalancerProfile",
				"networkProfile.podCidrs",
				"networkProfile.serviceCidrs",
				"nodeResourceGroup",
				"agentPoolProfiles[*].powerState",
				"privateLinkResources",
				"windowsProfile",
			}))
		if err != nil {
			return err
		}

		// Create each user pool as a separate AgentPool resource
		// This works for both new and legacy clusters:
		// - New clusters: all user pools are created here
		// - Legacy clusters: additional pools beyond the hardcoded "userpool" are created here
		if len(userNodePools) > 0 {
			for _, poolConfig := range userNodePools {
				initialCount := poolConfig.MinCount
				if poolConfig.InitialCount != nil {
					initialCount = *poolConfig.InitialCount
				}

				availabilityZones := pulumi.StringArray{
					pulumi.String("2"),
					pulumi.String("3"),
				}
				if len(poolConfig.AvailabilityZones) > 0 {
					availabilityZones = toPulumiStringArray(poolConfig.AvailabilityZones)
				}

				maxPods := 110
				if poolConfig.MaxPods != nil {
					maxPods = *poolConfig.MaxPods
				}

				osDiskSizeGB := 256 // P15 tier, aligned with Azure's per-disk pricing model
				if poolConfig.RootDiskSize != nil {
					osDiskSizeGB = *poolConfig.RootDiskSize
				}

				agentPoolArgs := &containerservice.AgentPoolArgs{
					AgentPoolName:     pulumi.String(poolConfig.Name),
					ResourceGroupName: pulumi.String(azTarget.ResourceGroupName()),
					ResourceName:      aksCluster.Name, // Reference to ManagedCluster

					AvailabilityZones:   availabilityZones,
					Count:               pulumi.Int(initialCount),
					EnableAutoScaling:   pulumi.Bool(poolConfig.EnableAutoScaling),
					EnableFIPS:          pulumi.Bool(false),
					EnableNodePublicIP:  pulumi.Bool(false),
					KubeletDiskType:     pulumi.String(containerservice.KubeletDiskTypeOS),
					MaxCount:            pulumi.Int(poolConfig.MaxCount),
					MaxPods:             pulumi.Int(maxPods),
					MinCount:            pulumi.Int(poolConfig.MinCount),
					Mode:                pulumi.String(containerservice.AgentPoolModeUser),
					OrchestratorVersion: pulumi.String(clusterConfig.KubernetesVersion),
					OsDiskSizeGB:        pulumi.Int(osDiskSizeGB),
					OsDiskType:          pulumi.String(containerservice.OSDiskTypeManaged),
					OsSKU:               pulumi.String(containerservice.OSSKUUbuntu),
					OsType:              pulumi.String(containerservice.OSTypeLinux),
					ScaleDownMode:       pulumi.String(containerservice.ScaleDownModeDelete),
					Tags: pulumi.StringMap{
						"Owner": pulumi.String("ptd"),
					},
					Type: pulumi.String(containerservice.AgentPoolTypeVirtualMachineScaleSets),
					UpgradeSettings: &containerservice.AgentPoolUpgradeSettingsArgs{
						MaxSurge: pulumi.String("10%"),
					},
					VmSize:       pulumi.String(poolConfig.VMSize),
					VnetSubnetID: pulumi.String(subnetId),
				}

				// Add taints if specified
				if len(poolConfig.NodeTaints) > 0 {
					agentPoolArgs.NodeTaints = toPulumiStringArray(poolConfig.NodeTaints)
				}

				// Add labels if specified
				if len(poolConfig.NodeLabels) > 0 {
					agentPoolArgs.NodeLabels = toPulumiStringMap(poolConfig.NodeLabels)
				}

				// Create the AgentPool resource
				_, err = containerservice.NewAgentPool(
					ctx,
					fmt.Sprintf("aksUserPool-%s-%s", release, poolConfig.Name),
					agentPoolArgs,
					pulumi.Parent(aksCluster), // Set ManagedCluster as parent
					pulumi.Protect(config.ProtectPersistentResources),
				)
				if err != nil {
					return fmt.Errorf("failed to create user agent pool %s: %w", poolConfig.Name, err)
				}
			}
		}
	}

	return nil
}

// getOutboundType converts the configuration outbound type to the Azure SDK constant
// Defaults to LoadBalancer if not specified
func getOutboundType(configValue string) containerservice.OutboundType {
	switch configValue {
	case "LoadBalancer":
		return containerservice.OutboundTypeLoadBalancer
	case "UserDefinedRouting":
		return containerservice.OutboundTypeUserDefinedRouting
	case "ManagedNatGateway":
		return containerservice.OutboundTypeManagedNATGateway
	case "AssignedNatGateway":
		return containerservice.OutboundTypeUserAssignedNATGateway
	default:
		return containerservice.OutboundTypeLoadBalancer
	}
}

func toPulumiStringArray(strs []string) pulumi.StringArray {
	result := make(pulumi.StringArray, len(strs))
	for i, s := range strs {
		result[i] = pulumi.String(s)
	}
	return result
}

func toPulumiStringMap(m map[string]string) pulumi.StringMap {
	result := make(pulumi.StringMap)
	for k, v := range m {
		result[k] = pulumi.String(v)
	}
	return result
}

func getPersistentStackOutputs(ctx context.Context, target types.Target) (auto.OutputMap, error) {
	creds, err := target.Credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get target credentials: %w", err)
	}

	envVars, err := prepareEnvVarsForPulumi(ctx, target, creds)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare env vars: %w", err)
	}
	persistentStack, err := ptdpulumi.NewPythonPulumiStack(
		ctx,
		"azure",
		"workload",
		"persistent",
		target.Name(),
		target.Region(),
		target.PulumiBackendUrl(),
		target.PulumiSecretsProviderKey(),
		envVars,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create persistent stack: %w", err)
	}

	outputs, err := persistentStack.Outputs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve persistent stack outputs: %w", err)
	}

	return outputs, nil
}
