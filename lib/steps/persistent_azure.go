package steps

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	azcompute "github.com/pulumi/pulumi-azure-native-sdk/compute/v3"
	azcontainerregistry "github.com/pulumi/pulumi-azure-native-sdk/containerregistry/v3"
	azpg "github.com/pulumi/pulumi-azure-native-sdk/dbforpostgresql/v3"
	azdns "github.com/pulumi/pulumi-azure-native-sdk/dns/v3"
	azkeyvault "github.com/pulumi/pulumi-azure-native-sdk/keyvault/v3"
	aznetapp "github.com/pulumi/pulumi-azure-native-sdk/netapp/v3"
	aznetwork "github.com/pulumi/pulumi-azure-native-sdk/network/v3"
	azprivatedns "github.com/pulumi/pulumi-azure-native-sdk/privatedns/v3"
	azstorage "github.com/pulumi/pulumi-azure-native-sdk/storage/v3"
	random "github.com/pulumi/pulumi-random/sdk/v4/go/random"
	tls "github.com/pulumi/pulumi-tls/sdk/v5/go/tls"

	"github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/types"
)

// persistentAzureWorkloadProjectName is the OLD Python Pulumi project name for the
// Azure workload persistent step. Used verbatim in alias URNs (the migration
// playbook forbids ctx.Project() in alias URNs).
const persistentAzureWorkloadProjectName = "ptd-azure-workload-persistent"

// persistentAzureWorkloadCompType is the Python ComponentResource type token for
// the Azure workload persistent step (single super().__init__).
const persistentAzureWorkloadCompType = "ptd:AzureWorkloadPersistent"

// persistentAzureBastionCompType is the AzureBastion ComponentResource type
// token. In live state the AzureBastion component is TOP-LEVEL (not nested under
// ptd:AzureWorkloadPersistent), so its children's old URNs are
// ptd:AzureBastion$<type>::<name>.
const persistentAzureBastionCompType = "ptd:AzureBastion"

// persistentAzureManagedByValue is the posit.team/managed-by tag value Python set
// on Azure workload persistent resources (the Python module __name__).
const persistentAzureManagedByValue = "ptd.pulumi_resources.azure_workload_persistent"

// dbAdminUsername mirrors azure_workload_persistent.DB_ADMIN_USERNAME.
const dbAdminUsername = "ptd_admin"

// azureNetappVolumeProducts mirrors AzureWorkloadPersistent._NETAPP_VOLUME_PRODUCTS:
// product → capacity field. Iterated in a stable order (sorted by product) for
// determinism; the capacity is resolved per-product below.
var azureNetappVolumeProducts = []string{"connect", "workbench", "workbench-shared"}

// azureWorkloadPersistentParams bundles pre-fetched data for the Azure workload
// persistent deploy function. Char-limited names are computed here (matching
// AzureWorkload naming properties EXACTLY) so the deploy is a pure builder.
type azureWorkloadPersistentParams struct {
	compoundName       string // <true_name>-<environment>
	region             string
	subscriptionID     string
	resourceGroupName  string // rsg-ptd-<sanitized>
	keyVaultName       string // kv-ptd-<name[:17]>
	storageAccountName string // stptd<no-hyphens[:19]> (state account; holds blob containers)
	cfg                types.AzureWorkloadConfig

	requiredTags map[string]string // resource_tags + true-name + environment + managed-by

	// vnetRsgName: network.vnet_rsg_name if set, else resourceGroupName.
	vnetRsgName string
	// vnetName: network.provisioned_vnet_name if set, else vnet-ptd-<compound>.
	vnetName string

	// Char-limited / convention names (from AzureWorkload naming properties).
	netappAccountName        string // naa-ptd-<compound>
	netappPoolName           string // nap-ptd-<compound>
	netappSnapshotPolicyName string // snp-ptd-<compound>
	netappBackupVaultName    string // bkv-ptd-<compound>
	netappBackupPolicyName   string // bkp-ptd-<compound>
	netappSubnetName         string // snet-ptd-<compound>-netapp
	appGatewaySubnetName     string // snet-ptd-<compound>-agw
	acrRegistry              string // crptd<part0><parts...>
	filesStorageAccountName  string // stptdfiles<no-hyphens[:14]>

	// bastionImageVersion is the latest Ubuntu jumpbox image version (pre-fetched
	// via the compute SDK; "latest" on failure, matching Python's default).
	bastionImageVersion string
}

// runAzureInlineGo is the Azure-workload entry point for the persistent step. It
// pre-fetches external data (config, char-limited names, the latest bastion VM
// image version) and dispatches to azureWorkloadPersistentDeploy.
func (s *PersistentStep) runAzureInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("persistent: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AzureWorkloadConfig)
	if !ok {
		return fmt.Errorf("persistent: expected AzureWorkloadConfig, got %T", rawConfig)
	}

	// Apply Python AzureWorkloadConfig dataclass defaults for fields not set in
	// ptd.yaml (Go zero-values would otherwise diff live resources / drop protect).
	cfg.ProtectPersistentResources = true // Python default True; never set false in config
	if cfg.BastionInstanceType == "" {
		cfg.BastionInstanceType = "Standard_B1s"
	}
	if cfg.NetappBackupRetentionDays == 0 {
		cfg.NetappBackupRetentionDays = 30
	}
	if cfg.NetappDailyBackupStartTime == "" {
		cfg.NetappDailyBackupStartTime = "02:00"
	}
	if cfg.NetappSnapshotsToKeep == 0 {
		cfg.NetappSnapshotsToKeep = 7
	}
	if cfg.NetappVolumeConnectCapacity == 0 {
		cfg.NetappVolumeConnectCapacity = 200
	}
	if cfg.NetappVolumeWorkbenchCapacity == 0 {
		cfg.NetappVolumeWorkbenchCapacity = 200
	}
	if cfg.NetappVolumeWorkbenchSharedCapacity == 0 {
		cfg.NetappVolumeWorkbenchSharedCapacity = 50
	}
	if cfg.PpmFileShareSizeGib == 0 {
		cfg.PpmFileShareSizeGib = 100
	}

	azTarget, ok := s.DstTarget.(azure.Target)
	if !ok {
		return fmt.Errorf("persistent: expected Azure target")
	}
	azCreds, err := azure.OnlyAzureCredentials(creds)
	if err != nil {
		return err
	}

	compoundName := s.DstTarget.Name()
	resourceGroupName := azTarget.ResourceGroupName()

	// vnet_rsg_name: network.vnet_rsg_name if set, else the workload resource group.
	vnetRsgName := resourceGroupName
	if cfg.Network.VnetRsgName != "" {
		vnetRsgName = cfg.Network.VnetRsgName
	}

	// vnet_name: provisioned_vnet_name if set, else vnet-ptd-<compound>.
	vnetName := fmt.Sprintf("vnet-ptd-%s", compoundName)
	if cfg.Network.ProvisionedVnetName != "" {
		vnetName = cfg.Network.ProvisionedVnetName
	}

	// required_tags = resource_tags | {true-name, environment} then + managed-by.
	trueName, environment := compoundName, ""
	if idx := lastDash(compoundName); idx >= 0 {
		trueName = compoundName[:idx]
		environment = compoundName[idx+1:]
	}
	requiredTags := map[string]string{}
	for k, v := range cfg.ResourceTags {
		requiredTags[k] = v
	}
	requiredTags["posit.team/true-name"] = trueName
	requiredTags["posit.team/environment"] = environment
	requiredTags["posit.team/managed-by"] = persistentAzureManagedByValue

	// Latest jumpbox image version. Python defaults to "latest" and overrides on a
	// successful SDK lookup; mirror that (do not hard-fail the whole step here).
	bastionImageVersion := "latest"
	if v, verr := azure.GetLatestVMImageVersion(
		ctx, azCreds, azTarget.SubscriptionID(), cfg.Region,
		"Canonical", "0001-com-ubuntu-server-jammy", "22_04-lts-gen2",
	); verr == nil && v != "" {
		bastionImageVersion = v
	}

	params := azureWorkloadPersistentParams{
		compoundName:             compoundName,
		region:                   cfg.Region,
		subscriptionID:           azTarget.SubscriptionID(),
		resourceGroupName:        resourceGroupName,
		keyVaultName:             azTarget.VaultName(),
		storageAccountName:       azTarget.StateBucketName(),
		cfg:                      cfg,
		requiredTags:             requiredTags,
		vnetRsgName:              vnetRsgName,
		vnetName:                 vnetName,
		netappAccountName:        fmt.Sprintf("naa-ptd-%s", compoundName),
		netappPoolName:           fmt.Sprintf("nap-ptd-%s", compoundName),
		netappSnapshotPolicyName: fmt.Sprintf("snp-ptd-%s", compoundName),
		netappBackupVaultName:    fmt.Sprintf("bkv-ptd-%s", compoundName),
		netappBackupPolicyName:   fmt.Sprintf("bkp-ptd-%s", compoundName),
		netappSubnetName:         fmt.Sprintf("snet-ptd-%s-netapp", compoundName),
		appGatewaySubnetName:     fmt.Sprintf("snet-ptd-%s-agw", compoundName),
		acrRegistry:              azureACRRegistryName(compoundName),
		filesStorageAccountName:  azureFilesStorageAccountName(compoundName),
		bastionImageVersion:      bastionImageVersion,
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, target types.Target) error {
		return azureWorkloadPersistentDeploy(pctx, target, params)
	}, envVars)
	if err != nil {
		return err
	}
	return s.runPersistentStack(ctx, stack, creds)
}

// lastDash returns the index of the last '-' in s, or -1.
func lastDash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '-' {
			return i
		}
	}
	return -1
}

// azureACRRegistryName replicates AzureWorkload.acr_registry:
// "crptd" + parts[0] + "".join(word.lower() for word in parts[1:]).
func azureACRRegistryName(compoundName string) string {
	parts := splitDash(compoundName)
	out := "crptd"
	if len(parts) > 0 {
		out += parts[0]
		for _, w := range parts[1:] {
			out += toLower(w)
		}
	}
	return out
}

// azureFilesStorageAccountName replicates AzureWorkload.azure_files_storage_account_name:
// "stptdfiles" + compound_name.lower().replace("-", "")[0:14].
func azureFilesStorageAccountName(compoundName string) string {
	clean := removeDashes(toLower(compoundName))
	if len(clean) > 14 {
		clean = clean[:14]
	}
	return "stptdfiles" + clean
}

func splitDash(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == '-' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(c)
		}
	}
	out = append(out, cur)
	return out
}

func removeDashes(s string) string {
	out := ""
	for _, c := range s {
		if c != '-' {
			out += string(c)
		}
	}
	return out
}

func toLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// azureWorkloadPersistentDeploy replicates AzureWorkloadPersistent.__init__ from
// python-pulumi/src/ptd/pulumi_resources/azure_workload_persistent.py. Resource
// logical names (first ctor arg) match the Python source verbatim. Every resource
// carries a pulumi.Aliases option pointing at the old Python URN under the
// ptd:AzureWorkloadPersistent component so existing state is adopted, not replaced.
func azureWorkloadPersistentDeploy(ctx *pulumi.Context, _ types.Target, params azureWorkloadPersistentParams) error {
	cn := params.compoundName
	tags := azureTagMap(params.requiredTags)
	protect := params.cfg.ProtectPersistentResources

	// componentURN is the old Python AzureWorkloadPersistent component URN. Direct
	// children alias to it via ParentURN.
	componentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s::%s",
		ctx.Stack(), persistentAzureWorkloadProjectName, persistentAzureWorkloadCompType, cn)
	withAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(componentURN)}})
	}

	// withPoolParentAlias: netapp volumes are created with parent=self.capacity_pool.
	poolParentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$azure-native:netapp:CapacityPool::%s",
		ctx.Stack(), persistentAzureWorkloadProjectName, persistentAzureWorkloadCompType, params.netappPoolName)
	withPoolParentAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(poolParentURN)}})
	}

	// withBastionAlias: bastion children were parented to the nested AzureBastion
	// component, so their old URN has the AzureBastion type in the chain.
	bastionParentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s::%s",
		ctx.Stack(), persistentAzureWorkloadProjectName, persistentAzureBastionCompType,
		fmt.Sprintf("bas-ptd-%s-bastion", cn))
	withBastionAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(bastionParentURN)}})
	}

	// ── VNet (create or adopt) + 6 subnets each with an NSG ────────────────────
	vnetID, privateSubnet, appGatewaySubnet, bastionSubnet, publicIP, err := azureBuildVNet(
		ctx, params, tags, protect, withAlias,
	)
	if err != nil {
		return fmt.Errorf("persistent: vnet: %w", err)
	}

	// ── NetApp account / pool / snapshot policy / backup vault + policy ────────
	netappAccount, capacityPool, snapshotPolicy, backupVault, backupPolicy, err := azureBuildNetappBase(
		ctx, params, tags, protect, withAlias,
	)
	if err != nil {
		return fmt.Errorf("persistent: netapp base: %w", err)
	}

	// ── NetApp volumes (only when automated_volume_provisioning) ───────────────
	if params.cfg.AutomatedVolumeProvisioning {
		if err := azureBuildNetappVolumes(
			ctx, params, tags, protect, capacityPool, snapshotPolicy, backupVault, backupPolicy,
			withPoolParentAlias,
		); err != nil {
			return fmt.Errorf("persistent: netapp volumes: %w", err)
		}
	}
	_ = netappAccount

	// ── Postgres: main + grafana servers ───────────────────────────────────────
	mainServer, err := azureBuildPostgresServer(ctx, params, tags, protect, cn, "Standard_B2s", vnetID, withAlias)
	if err != nil {
		return fmt.Errorf("persistent: postgres main: %w", err)
	}
	if _, err := azureBuildPostgresServer(ctx, params, tags, protect, cn+"-grafana", "Standard_B1ms", vnetID, withAlias); err != nil {
		return fmt.Errorf("persistent: postgres grafana: %w", err)
	}

	// ── Container registry ──────────────────────────────────────────────────────
	acrRegistry, err := azcontainerregistry.NewRegistry(ctx, params.acrRegistry, &azcontainerregistry.RegistryArgs{
		RegistryName:      pulumi.String(params.acrRegistry),
		AdminUserEnabled:  pulumi.Bool(false),
		Location:          pulumi.String(params.region),
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		Sku: &azcontainerregistry.SkuArgs{
			Name: pulumi.String("Standard"),
		},
		Tags: tags,
	}, protectOpt(protect), withAlias())
	if err != nil {
		return fmt.Errorf("persistent: acr: %w", err)
	}

	// ── Blob containers: chronicle, loki ────────────────────────────────────────
	if _, err := azureBuildBlobContainer(ctx, params, "chronicle", protect, withAlias); err != nil {
		return fmt.Errorf("persistent: chronicle container: %w", err)
	}
	if _, err := azureBuildBlobContainer(ctx, params, "loki", protect, withAlias); err != nil {
		return fmt.Errorf("persistent: loki container: %w", err)
	}

	// ── Azure Files storage account + private endpoint + DNS ───────────────────
	if err := azureBuildFilesStorageAccount(ctx, params, tags, protect, privateSubnet, withAlias); err != nil {
		return fmt.Errorf("persistent: files storage account: %w", err)
	}

	// ── DNS zones ───────────────────────────────────────────────────────────────
	if err := azureBuildDNSZones(ctx, params, tags, withAlias); err != nil {
		return fmt.Errorf("persistent: dns zones: %w", err)
	}

	// ── Bastion (nested AzureBastion component) ────────────────────────────────
	bastionHost, jumpboxHost, jumpboxKey, err := azureBuildBastion(
		ctx, params, tags, protect, bastionSubnet, privateSubnet, withAlias, withBastionAlias,
	)
	if err != nil {
		return fmt.Errorf("persistent: bastion: %w", err)
	}

	// ── Mimir password + key vault secret ──────────────────────────────────────
	mimirPassword, err := azureBuildMimirPassword(ctx, params, tags, protect, withAlias)
	if err != nil {
		return fmt.Errorf("persistent: mimir password: %w", err)
	}

	// ── Outputs (must match Python register_outputs verbatim) ──────────────────
	dbFQDN := mainServer.FullyQualifiedDomainName
	ctx.Export("db_domain", dbFQDN)
	ctx.Export("db_url", dbFQDN.ApplyT(func(n string) string {
		return fmt.Sprintf("postgres://%s/postgres?sslmode=require", n)
	}).(pulumi.StringOutput))
	ctx.Export("acr_name", acrRegistry.Name)
	ctx.Export("app_gateway_subnet_id", appGatewaySubnet.ID())
	ctx.Export("bastion_name", bastionHost.Name)
	ctx.Export("bastion_jumpbox_id", jumpboxHost.ID())
	ctx.Export("bastion_ssh_private_key", jumpboxKey.PrivateKeyOpenssh)
	ctx.Export("mimir_password", mimirPassword.Result)
	ctx.Export("private_subnet_name", privateSubnet.Name)
	ctx.Export("private_subnet_cidr", privateSubnet.AddressPrefix)

	// nat_gw_ip: public_ip.ip_address when the public subnet branch ran, else nil.
	if publicIP != nil {
		ctx.Export("nat_gw_ip", publicIP.IpAddress)
	} else {
		ctx.Export("nat_gw_ip", pulumi.String(""))
	}

	// vnet_name / vnet_cidr: created VNet's name/cidr, else the configured values.
	if params.cfg.Network.VnetCidr != "" && params.cfg.Network.ProvisionedVnetName == "" {
		ctx.Export("vnet_name", pulumi.String(params.vnetName))
		ctx.Export("vnet_cidr", pulumi.String(params.cfg.Network.VnetCidr))
	} else {
		ctx.Export("vnet_name", pulumi.String(params.cfg.Network.ProvisionedVnetName))
		ctx.Export("vnet_cidr", pulumi.String(params.cfg.Network.VnetCidr))
	}

	_ = vnetID
	return nil
}

// protectOpt returns pulumi.Protect(protect).
func protectOpt(protect bool) pulumi.ResourceOption {
	return pulumi.Protect(protect)
}

// azureTagMap converts a string→string tag map to a pulumi.StringMap.
func azureTagMap(tags map[string]string) pulumi.StringMap {
	out := pulumi.StringMap{}
	for k, v := range tags {
		// Azure tag keys cannot contain '/'. Mirror Python azure_tag_key_format,
		// which replaces '/' with ':' (e.g. posit.team/environment ->
		// posit.team:environment). Without this every Azure resource's tags churn.
		out[strings.ReplaceAll(k, "/", ":")] = pulumi.String(v)
	}
	return out
}

// azureDenyAllInboundRule / azureDenyAllOutboundRule mirror the priority-4000
// catch-all DENY rules Python attaches to most NSGs.
func azureDenyAllInboundRule() *aznetwork.SecurityRuleTypeArgs {
	return &aznetwork.SecurityRuleTypeArgs{
		Name:                     pulumi.StringPtr("InboundDenyAll"),
		Priority:                 pulumi.IntPtr(4000),
		Direction:                pulumi.String("Inbound"),
		Access:                   pulumi.String("Deny"),
		Protocol:                 pulumi.String("*"),
		SourcePortRange:          pulumi.StringPtr("*"),
		DestinationPortRange:     pulumi.StringPtr("*"),
		SourceAddressPrefix:      pulumi.StringPtr("*"),
		DestinationAddressPrefix: pulumi.StringPtr("*"),
	}
}

// azureBuildVNet creates (or adopts) the VNet and the 6 subnets, each with an NSG.
// It returns the vnet id, the private/app-gateway/bastion subnets, and the public
// IP (nil unless the public-subnet branch ran).
func azureBuildVNet(
	ctx *pulumi.Context,
	params azureWorkloadPersistentParams,
	tags pulumi.StringMap,
	protect bool,
	withAlias func() pulumi.ResourceOption,
) (pulumi.StringInput, *aznetwork.Subnet, *aznetwork.Subnet, *aznetwork.Subnet, *aznetwork.PublicIPAddress, error) {
	cn := params.compoundName
	net := params.cfg.Network

	var vnetID pulumi.StringInput
	// vnet is the created VirtualNetwork (nil when adopting an existing VNet). When
	// set, subnets are parented to it so their URN is VirtualNetwork$Subnet,
	// matching how Python created them (parent=self.vnet). When adopting, Python
	// used parent=None, so subnets stay top-level.
	var vnet *aznetwork.VirtualNetwork

	// Branch: adopt existing VNet (provisioned_vnet_name) vs create new (vnet_cidr).
	switch {
	case net.ProvisionedVnetName != "":
		info, err := aznetwork.LookupVirtualNetwork(ctx, &aznetwork.LookupVirtualNetworkArgs{
			ResourceGroupName:  params.vnetRsgName,
			VirtualNetworkName: params.vnetName,
		})
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("lookup existing vnet: %w", err)
		}
		existingID := ""
		if info.Id != nil {
			existingID = *info.Id
		}
		vnetID = pulumi.String(existingID)
	case net.VnetCidr != "":
		v, err := aznetwork.NewVirtualNetwork(ctx, params.vnetName, &aznetwork.VirtualNetworkArgs{
			VirtualNetworkName: pulumi.String(params.vnetName),
			ResourceGroupName:  pulumi.String(params.vnetRsgName),
			AddressSpace: &aznetwork.AddressSpaceArgs{
				AddressPrefixes: pulumi.StringArray{pulumi.String(net.VnetCidr)},
			},
			Location: pulumi.String(params.region),
			Tags:     tags,
		}, protectOpt(protect), withAlias())
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("create vnet: %w", err)
		}
		vnet = v
		vnetID = v.ID()
	}

	// subnetOpts: subnets are children of the created VNet (matches state URN
	// VirtualNetwork$Subnet); when adopting an existing VNet they stay top-level.
	subnetOpts := func() []pulumi.ResourceOption {
		opts := []pulumi.ResourceOption{protectOpt(protect)}
		if vnet != nil {
			opts = append(opts, pulumi.Parent(vnet))
		}
		return opts
	}

	// Public subnet branch: PublicIP + NatGateway + public subnet.
	var publicIP *aznetwork.PublicIPAddress
	var natGateway *aznetwork.NatGateway
	if net.PublicSubnetCidr != "" {
		pip, err := aznetwork.NewPublicIPAddress(ctx, fmt.Sprintf("pip-ptd-%s", cn), &aznetwork.PublicIPAddressArgs{
			ResourceGroupName:        pulumi.String(params.vnetRsgName),
			PublicIPAllocationMethod: pulumi.String("Static"),
			Sku: &aznetwork.PublicIPAddressSkuArgs{
				Name: pulumi.String("Standard"),
			},
		}, withAlias())
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("public ip: %w", err)
		}
		publicIP = pip

		ng, err := aznetwork.NewNatGateway(ctx, fmt.Sprintf("ng-ptd-%s", cn), &aznetwork.NatGatewayArgs{
			ResourceGroupName: pulumi.String(params.vnetRsgName),
			Sku: &aznetwork.NatGatewaySkuArgs{
				Name: pulumi.String("Standard"),
			},
			PublicIpAddresses: aznetwork.SubResourceArray{
				&aznetwork.SubResourceArgs{Id: pip.ID()},
			},
		}, withAlias())
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("nat gateway: %w", err)
		}
		natGateway = ng

		if _, err := aznetwork.NewSubnet(ctx, fmt.Sprintf("snet-ptd-%s-public", cn), &aznetwork.SubnetArgs{
			ResourceGroupName:  pulumi.String(params.vnetRsgName),
			VirtualNetworkName: pulumi.String(params.vnetName),
			AddressPrefix:      pulumi.String(net.PublicSubnetCidr),
		}, subnetOpts()...); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("public subnet: %w", err)
		}
	}

	// Private NSG + subnet (no rules on NSG; default vnet/LB inbound is sufficient).
	privateNSG, err := aznetwork.NewNetworkSecurityGroup(ctx, fmt.Sprintf("nsg-ptd-%s-private", cn), &aznetwork.NetworkSecurityGroupArgs{
		NetworkSecurityGroupName: pulumi.String(fmt.Sprintf("nsg-ptd-%s-private", cn)),
		ResourceGroupName:        pulumi.String(params.vnetRsgName),
		Location:                 pulumi.String(params.region),
	}, withAlias())
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("private nsg: %w", err)
	}

	privateSubnetArgs := &aznetwork.SubnetArgs{
		ResourceGroupName:  pulumi.String(params.vnetRsgName),
		VirtualNetworkName: pulumi.String(params.vnetName),
		AddressPrefix:      pulumi.String(net.PrivateSubnetCidr),
		ServiceEndpoints: aznetwork.ServiceEndpointPropertiesFormatArray{
			&aznetwork.ServiceEndpointPropertiesFormatArgs{
				Locations: pulumi.StringArray{pulumi.String(params.region)},
				Service:   pulumi.String("Microsoft.SQL"),
			},
			&aznetwork.ServiceEndpointPropertiesFormatArgs{
				Locations: pulumi.StringArray{pulumi.String(params.region)},
				Service:   pulumi.String("Microsoft.Storage"),
			},
		},
		NetworkSecurityGroup: &aznetwork.NetworkSecurityGroupTypeArgs{Id: privateNSG.ID()},
	}
	if net.PublicSubnetCidr != "" && natGateway != nil {
		privateSubnetArgs.NatGateway = &aznetwork.SubResourceArgs{Id: natGateway.ID()}
	}
	if net.PrivateSubnetRouteTableID != "" {
		privateSubnetArgs.RouteTable = &aznetwork.RouteTableTypeArgs{Id: pulumi.String(net.PrivateSubnetRouteTableID)}
	}
	privateSubnet, err := aznetwork.NewSubnet(ctx, fmt.Sprintf("snet-ptd-%s-private", cn), privateSubnetArgs,
		subnetOpts()...)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("private subnet: %w", err)
	}

	// DB NSG + subnet (delegated to flexibleServers).
	dbNSG, err := aznetwork.NewNetworkSecurityGroup(ctx, fmt.Sprintf("nsg-ptd-%s-db", cn), &aznetwork.NetworkSecurityGroupArgs{
		NetworkSecurityGroupName: pulumi.String(fmt.Sprintf("nsg-ptd-%s-db", cn)),
		ResourceGroupName:        pulumi.String(params.vnetRsgName),
		Location:                 pulumi.String(params.region),
		SecurityRules: aznetwork.SecurityRuleTypeArray{
			&aznetwork.SecurityRuleTypeArgs{
				Name:                     pulumi.StringPtr("InboundPostgres"),
				Priority:                 pulumi.IntPtr(1000),
				Direction:                pulumi.String("Inbound"),
				Access:                   pulumi.String("Allow"),
				Protocol:                 pulumi.String("Tcp"),
				SourcePortRange:          pulumi.StringPtr("*"),
				DestinationPortRange:     pulumi.StringPtr("5432"),
				SourceAddressPrefix:      pulumi.StringPtr("VirtualNetwork"),
				DestinationAddressPrefix: pulumi.StringPtr("VirtualNetwork"),
			},
			azureDenyAllInboundRule(),
		},
	}, withAlias())
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("db nsg: %w", err)
	}

	if _, err := aznetwork.NewSubnet(ctx, fmt.Sprintf("snet-ptd-%s-db", cn), &aznetwork.SubnetArgs{
		ResourceGroupName:  pulumi.String(params.vnetRsgName),
		VirtualNetworkName: pulumi.String(params.vnetName),
		AddressPrefix:      pulumi.String(net.DbSubnetCidr),
		Delegations: aznetwork.DelegationArray{
			&aznetwork.DelegationArgs{
				Name:        pulumi.StringPtr("postgresql"),
				ServiceName: pulumi.StringPtr("Microsoft.DBforPostgreSQL/flexibleServers"),
			},
		},
		NetworkSecurityGroup: &aznetwork.NetworkSecurityGroupTypeArgs{Id: dbNSG.ID()},
	}, subnetOpts()...); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("db subnet: %w", err)
	}

	// NetApp NSG + subnet (delegated to Microsoft.NetApp/volumes).
	netappNSG, err := aznetwork.NewNetworkSecurityGroup(ctx, fmt.Sprintf("nsg-ptd-%s-netapp", cn), &aznetwork.NetworkSecurityGroupArgs{
		NetworkSecurityGroupName: pulumi.String(fmt.Sprintf("nsg-ptd-%s-netapp", cn)),
		ResourceGroupName:        pulumi.String(params.vnetRsgName),
		Location:                 pulumi.String(params.region),
		SecurityRules: aznetwork.SecurityRuleTypeArray{
			&aznetwork.SecurityRuleTypeArgs{
				Name:                     pulumi.StringPtr("InboundVnet"),
				Priority:                 pulumi.IntPtr(1000),
				Direction:                pulumi.String("Inbound"),
				Access:                   pulumi.String("Allow"),
				Protocol:                 pulumi.String("Tcp"),
				SourcePortRange:          pulumi.StringPtr("*"),
				DestinationPortRange:     pulumi.StringPtr("*"),
				SourceAddressPrefix:      pulumi.StringPtr("VirtualNetwork"),
				DestinationAddressPrefix: pulumi.StringPtr("VirtualNetwork"),
			},
			azureDenyAllInboundRule(),
		},
	}, withAlias())
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("netapp nsg: %w", err)
	}

	if _, err := aznetwork.NewSubnet(ctx, params.netappSubnetName, &aznetwork.SubnetArgs{
		SubnetName:         pulumi.String(params.netappSubnetName),
		ResourceGroupName:  pulumi.String(params.vnetRsgName),
		VirtualNetworkName: pulumi.String(params.vnetName),
		AddressPrefix:      pulumi.String(net.NetAppSubnetCidr),
		Delegations: aznetwork.DelegationArray{
			&aznetwork.DelegationArgs{
				ServiceName: pulumi.StringPtr("Microsoft.NetApp/volumes"),
				Name:        pulumi.StringPtr(fmt.Sprintf("%s-netapp-delegation", cn)),
				Type:        pulumi.StringPtr("Microsoft.Network/virtualNetworks/subnets/delegations"),
				Actions: pulumi.StringArray{
					pulumi.String("Microsoft.Network/networkinterfaces/*"),
					pulumi.String("Microsoft.Network/virtualNetworks/subnets/join/action"),
				},
			},
		},
		NetworkSecurityGroup: &aznetwork.NetworkSecurityGroupTypeArgs{Id: netappNSG.ID()},
	}, subnetOpts()...); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("netapp subnet: %w", err)
	}

	// App Gateway NSG + subnet.
	appGatewayNSG, err := aznetwork.NewNetworkSecurityGroup(ctx, fmt.Sprintf("nsg-ptd-%s-app-gateway", cn), &aznetwork.NetworkSecurityGroupArgs{
		NetworkSecurityGroupName: pulumi.String(fmt.Sprintf("nsg-ptd-%s-app-gateway", cn)),
		ResourceGroupName:        pulumi.String(params.vnetRsgName),
		Location:                 pulumi.String(params.region),
		SecurityRules: aznetwork.SecurityRuleTypeArray{
			&aznetwork.SecurityRuleTypeArgs{
				Name:                     pulumi.StringPtr("InboundVnet"),
				Priority:                 pulumi.IntPtr(1000),
				Direction:                pulumi.String("Inbound"),
				Access:                   pulumi.String("Allow"),
				Protocol:                 pulumi.String("Tcp"),
				SourcePortRange:          pulumi.StringPtr("*"),
				DestinationPortRange:     pulumi.StringPtr("*"),
				SourceAddressPrefix:      pulumi.StringPtr("VirtualNetwork"),
				DestinationAddressPrefix: pulumi.StringPtr("VirtualNetwork"),
			},
			azureDenyAllInboundRule(),
		},
	}, withAlias())
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("app gateway nsg: %w", err)
	}

	appGatewaySubnet, err := aznetwork.NewSubnet(ctx, params.appGatewaySubnetName, &aznetwork.SubnetArgs{
		SubnetName:           pulumi.String(params.appGatewaySubnetName),
		ResourceGroupName:    pulumi.String(params.vnetRsgName),
		VirtualNetworkName:   pulumi.String(params.vnetName),
		AddressPrefix:        pulumi.String(net.AppGatewaySubnetCidr),
		NetworkSecurityGroup: &aznetwork.NetworkSecurityGroupTypeArgs{Id: appGatewayNSG.ID()},
	}, subnetOpts()...)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("app gateway subnet: %w", err)
	}

	// Bastion NSG + subnet (named "AzureBastionSubnet"; rules per Azure Bastion docs).
	bastionNSG, err := aznetwork.NewNetworkSecurityGroup(ctx, fmt.Sprintf("nsg-ptd-%s-bastion", cn), &aznetwork.NetworkSecurityGroupArgs{
		NetworkSecurityGroupName: pulumi.String(fmt.Sprintf("nsg-ptd-%s-bastion", cn)),
		ResourceGroupName:        pulumi.String(params.vnetRsgName),
		Location:                 pulumi.String(params.region),
		SecurityRules:            azureBastionNSGRules(),
	}, withAlias())
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("bastion nsg: %w", err)
	}

	bastionSubnet, err := aznetwork.NewSubnet(ctx, "AzureBastionSubnet", &aznetwork.SubnetArgs{
		ResourceGroupName:    pulumi.String(params.vnetRsgName),
		VirtualNetworkName:   pulumi.String(params.vnetName),
		AddressPrefix:        pulumi.String(net.BastionSubnetCidr),
		NetworkSecurityGroup: &aznetwork.NetworkSecurityGroupTypeArgs{Id: bastionNSG.ID()},
	}, subnetOpts()...)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("bastion subnet: %w", err)
	}

	return vnetID, privateSubnet, appGatewaySubnet, bastionSubnet, publicIP, nil
}

// azureBastionNSGRules mirrors the Azure Bastion NSG rule set from _define_vnet.
func azureBastionNSGRules() aznetwork.SecurityRuleTypeArray {
	rule := func(name string, priority int, direction, access, proto, srcPort string, dstPort string, dstPorts []string, srcPrefix, dstPrefix string) *aznetwork.SecurityRuleTypeArgs {
		a := &aznetwork.SecurityRuleTypeArgs{
			Name:                     pulumi.StringPtr(name),
			Priority:                 pulumi.IntPtr(priority),
			Direction:                pulumi.String(direction),
			Access:                   pulumi.String(access),
			Protocol:                 pulumi.String(proto),
			SourcePortRange:          pulumi.StringPtr(srcPort),
			SourceAddressPrefix:      pulumi.StringPtr(srcPrefix),
			DestinationAddressPrefix: pulumi.StringPtr(dstPrefix),
		}
		if len(dstPorts) > 0 {
			arr := pulumi.StringArray{}
			for _, p := range dstPorts {
				arr = append(arr, pulumi.String(p))
			}
			a.DestinationPortRanges = arr
		} else {
			a.DestinationPortRange = pulumi.StringPtr(dstPort)
		}
		return a
	}

	return aznetwork.SecurityRuleTypeArray{
		// Inbound
		rule("AllowHttpsInboundFromInternet", 100, "Inbound", "Allow", "Tcp", "*", "443", nil, "Internet", "*"),
		rule("AllowGatewayManagerInbound", 110, "Inbound", "Allow", "Tcp", "*", "443", nil, "GatewayManager", "*"),
		rule("AllowVnetInbound", 120, "Inbound", "Allow", "Tcp", "*", "", []string{"8080", "5701"}, "VirtualNetwork", "VirtualNetwork"),
		rule("AllowAzureLoadBalancerInbound", 130, "Inbound", "Allow", "Tcp", "*", "443", nil, "AzureLoadBalancer", "*"),
		rule("DenyAllInbound", 4000, "Inbound", "Deny", "*", "*", "*", nil, "*", "*"),
		// Outbound
		rule("AllowSshRdpOutbound", 100, "Outbound", "Allow", "Tcp", "*", "", []string{"22", "3389"}, "*", "VirtualNetwork"),
		rule("AllowVnetOutbound", 110, "Outbound", "Allow", "Tcp", "*", "", []string{"8080", "5701"}, "VirtualNetwork", "VirtualNetwork"),
		rule("AllowAzureCloudOutbound", 120, "Outbound", "Allow", "Tcp", "*", "443", nil, "*", "AzureCloud"),
		rule("AllowInternetOutbound", 130, "Outbound", "Allow", "Tcp", "*", "80", nil, "*", "Internet"),
		rule("DenyAllOutbound", 4000, "Outbound", "Deny", "*", "*", "*", nil, "*", "*"),
	}
}

// azureBuildNetappBase mirrors _define_file_storage: account, capacity pool,
// snapshot policy, backup vault, backup policy.
func azureBuildNetappBase(
	ctx *pulumi.Context,
	params azureWorkloadPersistentParams,
	tags pulumi.StringMap,
	protect bool,
	withAlias func() pulumi.ResourceOption,
) (*aznetapp.Account, *aznetapp.CapacityPool, *aznetapp.SnapshotPolicy, *aznetapp.BackupVault, *aznetapp.BackupPolicy, error) {
	account, err := aznetapp.NewAccount(ctx, params.netappAccountName, &aznetapp.AccountArgs{
		AccountName:       pulumi.String(params.netappAccountName),
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		Location:          pulumi.String(params.region),
	}, protectOpt(protect), withAlias())
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("netapp account: %w", err)
	}

	pool, err := aznetapp.NewCapacityPool(ctx, params.netappPoolName, &aznetapp.CapacityPoolArgs{
		PoolName:          pulumi.String(params.netappPoolName),
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		AccountName:       account.Name,
		Location:          pulumi.String(params.region),
		ServiceLevel:      pulumi.String("Premium"),
		Size:              pulumi.Float64(1099511627776), // 1 TiB (min requirement)
	}, protectOpt(protect), withAlias())
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("capacity pool: %w", err)
	}

	hour, minute, err := parseBackupStartTime(params.cfg.NetappDailyBackupStartTime)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	snapshotsToKeep := params.cfg.NetappSnapshotsToKeep
	if snapshotsToKeep == 0 {
		snapshotsToKeep = 7
	}
	snapshotPolicy, err := aznetapp.NewSnapshotPolicy(ctx, params.netappSnapshotPolicyName, &aznetapp.SnapshotPolicyArgs{
		SnapshotPolicyName: pulumi.String(params.netappSnapshotPolicyName),
		ResourceGroupName:  pulumi.String(params.resourceGroupName),
		AccountName:        account.Name,
		Location:           pulumi.String(params.region),
		Enabled:            pulumi.Bool(true),
		DailySchedule: &aznetapp.DailyScheduleArgs{
			Hour:            pulumi.Int(hour),
			Minute:          pulumi.Int(minute),
			SnapshotsToKeep: pulumi.Int(snapshotsToKeep),
		},
		Tags: tags,
	}, protectOpt(protect), withAlias())
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("snapshot policy: %w", err)
	}

	backupVault, err := aznetapp.NewBackupVault(ctx, params.netappBackupVaultName, &aznetapp.BackupVaultArgs{
		BackupVaultName:   pulumi.String(params.netappBackupVaultName),
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		AccountName:       account.Name,
		Location:          pulumi.String(params.region),
		Tags:              tags,
	}, protectOpt(protect), withAlias())
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("backup vault: %w", err)
	}

	retention := params.cfg.NetappBackupRetentionDays
	if retention == 0 {
		retention = 30
	}
	backupPolicy, err := aznetapp.NewBackupPolicy(ctx, params.netappBackupPolicyName, &aznetapp.BackupPolicyArgs{
		BackupPolicyName:   pulumi.String(params.netappBackupPolicyName),
		ResourceGroupName:  pulumi.String(params.resourceGroupName),
		AccountName:        account.Name,
		Location:           pulumi.String(params.region),
		Enabled:            pulumi.Bool(true),
		DailyBackupsToKeep: pulumi.Int(retention),
		Tags:               tags,
	}, protectOpt(protect), withAlias())
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("backup policy: %w", err)
	}

	return account, pool, snapshotPolicy, backupVault, backupPolicy, nil
}

// parseBackupStartTime parses "HH:MM" into (hour, minute), mirroring the Python
// validation in _define_file_storage.
func parseBackupStartTime(t string) (int, int, error) {
	if t == "" {
		t = "02:00"
	}
	var h, m int
	parts := splitColon(t)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("netapp_daily_backup_start_time must be HH:MM, got %q", t)
	}
	var err error
	if h, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, fmt.Errorf("netapp_daily_backup_start_time must be HH:MM, got %q", t)
	}
	if m, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, fmt.Errorf("netapp_daily_backup_start_time must be HH:MM, got %q", t)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("netapp_daily_backup_start_time must be HH:MM, got %q", t)
	}
	return h, m, nil
}

func splitColon(s string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

// azureBuildNetappVolumes mirrors _define_netapp_volumes: one volume per
// site × product, with the workbench-shared no-data-protection sub-branch.
func azureBuildNetappVolumes(
	ctx *pulumi.Context,
	params azureWorkloadPersistentParams,
	tags pulumi.StringMap,
	protect bool,
	pool *aznetapp.CapacityPool,
	snapshotPolicy *aznetapp.SnapshotPolicy,
	backupVault *aznetapp.BackupVault,
	backupPolicy *aznetapp.BackupPolicy,
	withPoolParentAlias func() pulumi.ResourceOption,
) error {
	connectCap := params.cfg.NetappVolumeConnectCapacity
	if connectCap == 0 {
		connectCap = 200
	}
	workbenchCap := params.cfg.NetappVolumeWorkbenchCapacity
	if workbenchCap == 0 {
		workbenchCap = 200
	}
	workbenchSharedCap := params.cfg.NetappVolumeWorkbenchSharedCapacity
	if workbenchSharedCap == 0 {
		workbenchSharedCap = 50
	}
	capacityFor := map[string]int{
		"connect":          connectCap,
		"workbench":        workbenchCap,
		"workbench-shared": workbenchSharedCap,
	}

	siteNames := make([]string, 0, len(params.cfg.Sites))
	for s := range params.cfg.Sites {
		siteNames = append(siteNames, s)
	}
	sort.Strings(siteNames)

	for _, siteName := range siteNames {
		for _, product := range azureNetappVolumeProducts {
			volumeName := fmt.Sprintf("nav-ptd-%s-%s", siteName, product)
			capacityGib := capacityFor[product]

			var dataProtection aznetapp.VolumePropertiesDataProtectionPtrInput
			if product != "workbench-shared" {
				dataProtection = &aznetapp.VolumePropertiesDataProtectionArgs{
					Backup: &aznetapp.VolumeBackupPropertiesArgs{
						BackupPolicyId: backupPolicy.ID(),
						BackupVaultId:  backupVault.ID(),
						PolicyEnforced: pulumi.Bool(true),
					},
					Snapshot: &aznetapp.VolumeSnapshotPropertiesArgs{
						SnapshotPolicyId: snapshotPolicy.ID(),
					},
				}
			}

			args := &aznetapp.CapacityPoolVolumeArgs{
				VolumeName:        pulumi.String(volumeName),
				ResourceGroupName: pulumi.String(params.resourceGroupName),
				AccountName:       pulumi.String(params.netappAccountName),
				PoolName:          pulumi.String(params.netappPoolName),
				Location:          pulumi.String(params.region),
				ServiceLevel:      pulumi.String("Premium"),
				SubnetId:          azureNetappSubnetID(params),
				UsageThreshold:    pulumi.Float64(float64(capacityGib) * 1024 * 1024 * 1024),
				CreationToken:     pulumi.String(volumeName),
				ProtocolTypes:     pulumi.StringArray{pulumi.String("NFSv3")},
				NetworkFeatures:   pulumi.String("Standard"),
				UnixPermissions:   pulumi.String("0755"),
				ExportPolicy: &aznetapp.VolumePropertiesExportPolicyArgs{
					Rules: aznetapp.ExportPolicyRuleArray{
						&aznetapp.ExportPolicyRuleArgs{
							RuleIndex:      pulumi.Int(1),
							AllowedClients: pulumi.String("0.0.0.0/0"),
							Nfsv3:          pulumi.Bool(true),
							Nfsv41:         pulumi.Bool(false),
							UnixReadOnly:   pulumi.Bool(false),
							UnixReadWrite:  pulumi.Bool(true),
							HasRootAccess:  pulumi.Bool(true),
						},
					},
				},
				Tags: tags,
			}
			if dataProtection != nil {
				args.DataProtection = dataProtection
			}

			if _, err := aznetapp.NewCapacityPoolVolume(ctx, volumeName, args,
				pulumi.Parent(pool),
				protectOpt(protect),
				withPoolParentAlias(),
			); err != nil {
				return fmt.Errorf("netapp volume %s: %w", volumeName, err)
			}
		}
	}
	return nil
}

// azureNetappSubnetID constructs the netapp subnet resource id from convention
// names (the Python code referenced self.netapp_subnet.id; the subnet is created
// in azureBuildVNet but the volume only needs its resource id by name).
func azureNetappSubnetID(params azureWorkloadPersistentParams) pulumi.StringInput {
	return pulumi.String(fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s",
		params.subscriptionID, params.vnetRsgName, params.vnetName, params.netappSubnetName,
	))
}

// azureDBSubnetID constructs the db subnet resource id (snet-ptd-<compound>-db)
// from convention names (the Python code referenced self.db_subnet.id).
func azureDBSubnetID(params azureWorkloadPersistentParams) pulumi.StringInput {
	return pulumi.String(fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/snet-ptd-%s-db",
		params.subscriptionID, params.vnetRsgName, params.vnetName, params.compoundName,
	))
}

// azureBuildPostgresServer mirrors _define_database_resources: random password,
// private DNS zone + vnet link, the flexible server, and a Key Vault secret.
func azureBuildPostgresServer(
	ctx *pulumi.Context,
	params azureWorkloadPersistentParams,
	tags pulumi.StringMap,
	protect bool,
	name string,
	dbInstanceType string,
	vnetID pulumi.StringInput,
	withAlias func() pulumi.ResourceOption,
) (*azpg.Server, error) {
	pw, err := random.NewRandomPassword(ctx, fmt.Sprintf("%s-db-pw", name), &random.RandomPasswordArgs{
		Special:         pulumi.Bool(true),
		OverrideSpecial: pulumi.String("-_"),
		Length:          pulumi.Int(36),
	}, withAlias())
	if err != nil {
		return nil, fmt.Errorf("db password: %w", err)
	}

	dnsZone, err := azprivatedns.NewPrivateZone(ctx, fmt.Sprintf("%s-private-dns-zone", name), &azprivatedns.PrivateZoneArgs{
		Location:          pulumi.String("Global"),
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		PrivateZoneName:   pulumi.String(fmt.Sprintf("%s.ptd.postgres.database.azure.com", name)),
		Tags:              tags,
	}, protectOpt(protect), withAlias())
	if err != nil {
		return nil, fmt.Errorf("private dns zone: %w", err)
	}

	if _, err := azprivatedns.NewVirtualNetworkLink(ctx, fmt.Sprintf("%s-dns-vnet-link", name), &azprivatedns.VirtualNetworkLinkArgs{
		ResourceGroupName:      pulumi.String(params.resourceGroupName),
		VirtualNetworkLinkName: pulumi.String(fmt.Sprintf("%s-dns-vnet-link", name)),
		Location:               pulumi.String("global"),
		PrivateZoneName:        dnsZone.Name,
		RegistrationEnabled:    pulumi.Bool(false),
		VirtualNetwork:         &azprivatedns.SubResourceArgs{Id: vnetID},
		Tags:                   tags,
	}, withAlias()); err != nil {
		return nil, fmt.Errorf("dns vnet link: %w", err)
	}

	server, err := azpg.NewServer(ctx, fmt.Sprintf("psql-ptd-%s", name), &azpg.ServerArgs{
		AdministratorLogin:         pulumi.String(dbAdminUsername),
		AdministratorLoginPassword: pw.Result,
		Location:                   pulumi.String(params.region),
		DataEncryption: &azpg.DataEncryptionArgs{
			Type: pulumi.String("SystemManaged"),
		},
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		ServerName:        pulumi.String(fmt.Sprintf("psql-ptd-%s", name)),
		Network: &azpg.NetworkArgs{
			// Python uses self.db_subnet.id; reference the db subnet by its
			// convention resource id (snet-ptd-<compound>-db).
			DelegatedSubnetResourceId:   azureDBSubnetID(params),
			PrivateDnsZoneArmResourceId: dnsZone.ID(),
		},
		Sku: &azpg.SkuArgs{
			Name: pulumi.String(dbInstanceType),
			Tier: pulumi.String("Burstable"),
		},
		Storage: &azpg.StorageArgs{
			AutoGrow:      pulumi.String("Enabled"),
			Tier:          pulumi.String("P10"),
			StorageSizeGB: pulumi.Int(128),
			Type:          pulumi.String("Premium_LRS"),
		},
		Version: pulumi.String("14"),
		Tags:    tags,
		// administratorLoginPassword is write-only (Azure never returns it). Go's
		// RandomPassword is fresh on the first Go apply (Python's value lived in
		// Python state), so sending it would rotate the live DB password. Ignore it
		// to keep the existing password; greenfield still sets it on create.
	}, protectOpt(protect), withAlias(), pulumi.IgnoreChanges([]string{"administratorLoginPassword"}))
	if err != nil {
		return nil, fmt.Errorf("postgres server: %w", err)
	}

	// Key Vault secret with {fqdn, username, password}.
	secretVal := pulumi.All(pw.Result, server.FullyQualifiedDomainName).ApplyT(func(args []interface{}) (string, error) {
		password, _ := args[0].(string)
		fqdn, _ := args[1].(string)
		return jsonMarshal(map[string]string{
			"fqdn":     fqdn,
			"username": dbAdminUsername,
			"password": password,
		})
	}).(pulumi.StringOutput)

	if _, err := azkeyvault.NewSecret(ctx, fmt.Sprintf("%s-postgres-admin-secret", name), &azkeyvault.SecretArgs{
		SecretName:        pulumi.String(fmt.Sprintf("%s-postgres-admin-secret", name)),
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		Properties: &azkeyvault.SecretPropertiesArgs{
			Value: secretVal,
		},
		VaultName: pulumi.String(params.keyVaultName),
		Tags:      tags,
		// The secret embeds the DB password (above). Since Go's RandomPassword
		// differs from the live/Python value, ignore the stored value on adoption
		// so we don't rotate the secret to a password the server doesn't have.
		// Greenfield still writes the value on create.
	}, protectOpt(protect), withAlias(), pulumi.IgnoreChanges([]string{"properties.value"})); err != nil {
		return nil, fmt.Errorf("postgres admin secret: %w", err)
	}

	return server, nil
}

// azureBuildBlobContainer mirrors _create_blob_container.
func azureBuildBlobContainer(
	ctx *pulumi.Context,
	params azureWorkloadPersistentParams,
	containerName string,
	protect bool,
	withAlias func() pulumi.ResourceOption,
) (*azstorage.BlobContainer, error) {
	c, err := azstorage.NewBlobContainer(ctx, fmt.Sprintf("%s-%s-container", params.compoundName, containerName), &azstorage.BlobContainerArgs{
		AccountName:       pulumi.String(params.storageAccountName),
		ContainerName:     pulumi.String(containerName),
		ResourceGroupName: pulumi.String(params.resourceGroupName),
	}, protectOpt(protect), withAlias())
	if err != nil {
		return nil, err
	}
	return c, nil
}

// azureBuildFilesStorageAccount mirrors _define_files_storage_account.
func azureBuildFilesStorageAccount(
	ctx *pulumi.Context,
	params azureWorkloadPersistentParams,
	tags pulumi.StringMap,
	protect bool,
	privateSubnet *aznetwork.Subnet,
	withAlias func() pulumi.ResourceOption,
) error {
	cn := params.compoundName

	privateDNSZone, err := azprivatedns.NewPrivateZone(ctx, fmt.Sprintf("%s-files-dns-zone", cn), &azprivatedns.PrivateZoneArgs{
		Location:          pulumi.String("Global"),
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		PrivateZoneName:   pulumi.String("privatelink.file.core.windows.net"),
		Tags:              tags,
	}, protectOpt(protect), withAlias())
	if err != nil {
		return fmt.Errorf("files dns zone: %w", err)
	}

	vnetDNSLink, err := azprivatedns.NewVirtualNetworkLink(ctx, fmt.Sprintf("%s-files-dns-link", cn), &azprivatedns.VirtualNetworkLinkArgs{
		ResourceGroupName:      pulumi.String(params.resourceGroupName),
		VirtualNetworkLinkName: pulumi.String(fmt.Sprintf("%s-files-dns-link", cn)),
		Location:               pulumi.String("global"),
		PrivateZoneName:        privateDNSZone.Name,
		RegistrationEnabled:    pulumi.Bool(false),
		ResolutionPolicy:       pulumi.String("NxDomainRedirect"),
		VirtualNetwork:         &azprivatedns.SubResourceArgs{Id: privateSubnetVNetID(params)},
		Tags:                   tags,
	}, protectOpt(protect), withAlias())
	if err != nil {
		return fmt.Errorf("files dns link: %w", err)
	}

	storageAccount, err := azstorage.NewStorageAccount(ctx, fmt.Sprintf("%s-files-storage", cn), &azstorage.StorageAccountArgs{
		AccountName:            pulumi.String(params.filesStorageAccountName),
		ResourceGroupName:      pulumi.String(params.resourceGroupName),
		Location:               pulumi.String(params.region),
		Kind:                   pulumi.String("FileStorage"),
		EnableHttpsTrafficOnly: pulumi.Bool(false),
		Sku: &azstorage.SkuArgs{
			Name: pulumi.String("Premium_LRS"),
		},
		MinimumTlsVersion: pulumi.String("TLS1_2"),
		NetworkRuleSet: &azstorage.NetworkRuleSetArgs{
			DefaultAction: azstorage.DefaultActionDeny,
			VirtualNetworkRules: azstorage.VirtualNetworkRuleArray{
				&azstorage.VirtualNetworkRuleArgs{
					VirtualNetworkResourceId: privateSubnet.ID(),
					Action:                   azstorage.ActionAllow,
				},
			},
			Bypass: pulumi.String("AzureServices"),
		},
		Tags: tags,
	}, protectOpt(protect), pulumi.DependsOn([]pulumi.Resource{vnetDNSLink}), withAlias())
	if err != nil {
		return fmt.Errorf("files storage account: %w", err)
	}

	privateEndpoint, err := aznetwork.NewPrivateEndpoint(ctx, fmt.Sprintf("%s-files-pe", cn), &aznetwork.PrivateEndpointArgs{
		PrivateEndpointName: pulumi.String(fmt.Sprintf("%s-files-pe", cn)),
		ResourceGroupName:   pulumi.String(params.resourceGroupName),
		Location:            pulumi.String(params.region),
		Subnet:              &aznetwork.SubnetTypeArgs{Id: privateSubnet.ID()},
		PrivateLinkServiceConnections: aznetwork.PrivateLinkServiceConnectionArray{
			&aznetwork.PrivateLinkServiceConnectionArgs{
				Name:                 pulumi.String(fmt.Sprintf("%s-files-plsc", cn)),
				PrivateLinkServiceId: storageAccount.ID(),
				GroupIds:             pulumi.StringArray{pulumi.String("file")},
			},
		},
		Tags: tags,
	}, protectOpt(protect), pulumi.DependsOn([]pulumi.Resource{storageAccount}), withAlias(),
		// subnet is ForceNew. The newer azure-native provider auto-populates
		// subnet.privateEndpointNetworkPolicies / privateLinkServiceNetworkPolicies
		// (absent from older state), which would otherwise force a destructive
		// replace of the live private endpoint (and cascade to its DNS zone group).
		// The subnet id is unchanged, so ignore the subnet field — the
		// provider-upgrade ForceNew pattern.
		pulumi.IgnoreChanges([]string{"subnet"}))
	if err != nil {
		return fmt.Errorf("files private endpoint: %w", err)
	}

	if _, err := aznetwork.NewPrivateDnsZoneGroup(ctx, fmt.Sprintf("%s-files-dns-zone-group", cn), &aznetwork.PrivateDnsZoneGroupArgs{
		PrivateDnsZoneGroupName: pulumi.String("default"),
		PrivateEndpointName:     privateEndpoint.Name,
		ResourceGroupName:       pulumi.String(params.resourceGroupName),
		PrivateDnsZoneConfigs: aznetwork.PrivateDnsZoneConfigArray{
			&aznetwork.PrivateDnsZoneConfigArgs{
				Name:             pulumi.String("privatelink-file-core-windows-net"),
				PrivateDnsZoneId: privateDNSZone.ID(),
			},
		},
	}, protectOpt(protect), pulumi.DependsOn([]pulumi.Resource{privateEndpoint}), withAlias()); err != nil {
		return fmt.Errorf("files private dns zone group: %w", err)
	}

	return nil
}

// privateSubnetVNetID returns the VNet resource id (the files dns link binds the
// VNet, not the subnet). Constructed from convention names.
func privateSubnetVNetID(params azureWorkloadPersistentParams) pulumi.StringInput {
	return pulumi.String(fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s",
		params.subscriptionID, params.vnetRsgName, params.vnetName,
	))
}

// azureBuildDNSZones mirrors _define_dns_zones: a single zone for root_domain, or
// one zone per site domain.
func azureBuildDNSZones(
	ctx *pulumi.Context,
	params azureWorkloadPersistentParams,
	tags pulumi.StringMap,
	withAlias func() pulumi.ResourceOption,
) error {
	cn := params.compoundName
	if params.cfg.RootDomain != nil && *params.cfg.RootDomain != "" {
		if _, err := azdns.NewZone(ctx, fmt.Sprintf("%s-dns-zone", cn), &azdns.ZoneArgs{
			Location:          pulumi.String("Global"),
			ResourceGroupName: pulumi.String(params.resourceGroupName),
			ZoneName:          pulumi.String(*params.cfg.RootDomain),
			Tags:              tags,
		}, withAlias()); err != nil {
			return fmt.Errorf("root dns zone: %w", err)
		}
		return nil
	}

	siteNames := make([]string, 0, len(params.cfg.Sites))
	for s := range params.cfg.Sites {
		siteNames = append(siteNames, s)
	}
	sort.Strings(siteNames)
	for _, siteName := range siteNames {
		site := params.cfg.Sites[siteName]
		if _, err := azdns.NewZone(ctx, fmt.Sprintf("%s-%s-dns-zone", cn, siteName), &azdns.ZoneArgs{
			Location:          pulumi.String("Global"),
			ResourceGroupName: pulumi.String(params.resourceGroupName),
			ZoneName:          pulumi.String(site.Spec.Domain),
			Tags:              tags,
		}, withAlias()); err != nil {
			return fmt.Errorf("site dns zone %s: %w", siteName, err)
		}
	}
	return nil
}

// azureBuildBastion mirrors _define_bastion + AzureBastion: ssh key, public ip,
// bastion host, jumpbox NIC, jumpbox VM. Bastion children are aliased to the
// nested ptd:AzureBastion component URN.
func azureBuildBastion(
	ctx *pulumi.Context,
	params azureWorkloadPersistentParams,
	tags pulumi.StringMap,
	protect bool,
	bastionSubnet *aznetwork.Subnet,
	jumpboxSubnet *aznetwork.Subnet,
	withAlias func() pulumi.ResourceOption,
	withBastionAlias func() pulumi.ResourceOption,
) (*aznetwork.BastionHost, *azcompute.VirtualMachine, *tls.PrivateKey, error) {
	cn := params.compoundName
	name := fmt.Sprintf("bas-ptd-%s-bastion", cn)
	vmSize := params.cfg.BastionInstanceType
	if vmSize == "" {
		vmSize = "Standard_B1s"
	}

	// ssh-key (logical name "ssh-key", no parent in Python beyond the component).
	sshKey, err := tls.NewPrivateKey(ctx, "ssh-key", &tls.PrivateKeyArgs{
		Algorithm: pulumi.String("ED25519"),
	}, withBastionAlias())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ssh key: %w", err)
	}

	publicIP, err := aznetwork.NewPublicIPAddress(ctx, fmt.Sprintf("%s-pip", name), &aznetwork.PublicIPAddressArgs{
		ResourceGroupName:        pulumi.String(params.vnetRsgName),
		PublicIPAllocationMethod: pulumi.String("Static"),
		Sku: &aznetwork.PublicIPAddressSkuArgs{
			Name: pulumi.String("Standard"),
		},
		Tags: azureTagMergeName(tags, fmt.Sprintf("%s-pip", name)),
	}, withBastionAlias())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bastion public ip: %w", err)
	}

	bastionHost, err := aznetwork.NewBastionHost(ctx, fmt.Sprintf("%s-host", name), &aznetwork.BastionHostArgs{
		ResourceGroupName: pulumi.String(params.vnetRsgName),
		Location:          pulumi.String(params.region),
		IpConfigurations: aznetwork.BastionHostIPConfigurationArray{
			&aznetwork.BastionHostIPConfigurationArgs{
				Name:            pulumi.String("bastionIpConfig"),
				PublicIPAddress: &aznetwork.SubResourceArgs{Id: publicIP.ID()},
				Subnet:          &aznetwork.SubResourceArgs{Id: bastionSubnet.ID()},
			},
		},
		EnableTunneling: pulumi.Bool(true),
		Sku: &aznetwork.SkuArgs{
			Name: pulumi.String("Standard"),
		},
		Tags: azureTagMergeName(tags, fmt.Sprintf("%s-bastion-host", name)),
	}, withBastionAlias())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bastion host: %w", err)
	}

	jumpboxNIC, err := aznetwork.NewNetworkInterface(ctx, fmt.Sprintf("%s-jumpbox-nic", name), &aznetwork.NetworkInterfaceArgs{
		ResourceGroupName: pulumi.String(params.vnetRsgName),
		Location:          pulumi.String(params.region),
		IpConfigurations: aznetwork.NetworkInterfaceIPConfigurationArray{
			&aznetwork.NetworkInterfaceIPConfigurationArgs{
				Name:   pulumi.String("internal"),
				Subnet: &aznetwork.SubnetTypeArgs{Id: jumpboxSubnet.ID()},
			},
		},
		// Same provider-upgrade subnet churn as the PrivateEndpoint: the newer
		// azure-native provider populates subnet.{privateEndpoint,privateLinkService}
		// NetworkPolicies in the NIC's inline subnet view. The subnet id is
		// unchanged; ignore it so the bastion NIC doesn't churn on every apply.
	}, withBastionAlias(), pulumi.IgnoreChanges([]string{"ipConfigurations[0].subnet"}))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("jumpbox nic: %w", err)
	}

	jumpboxTags := azureTagMergeName(tags, fmt.Sprintf("%s-jumpbox", name))
	jumpboxTags["ImageVersion"] = pulumi.String(params.bastionImageVersion)

	jumpbox, err := azcompute.NewVirtualMachine(ctx, fmt.Sprintf("%s-jumpbox", name), &azcompute.VirtualMachineArgs{
		ResourceGroupName: pulumi.String(params.vnetRsgName),
		Location:          pulumi.String(params.region),
		HardwareProfile: &azcompute.HardwareProfileArgs{
			VmSize: pulumi.String(vmSize),
		},
		StorageProfile: &azcompute.StorageProfileArgs{
			ImageReference: &azcompute.ImageReferenceArgs{
				Publisher: pulumi.String("Canonical"),
				Offer:     pulumi.String("0001-com-ubuntu-server-jammy"),
				Sku:       pulumi.String("22_04-lts-gen2"),
				Version:   pulumi.String(params.bastionImageVersion),
			},
			OsDisk: &azcompute.OSDiskArgs{
				Name:         pulumi.String(fmt.Sprintf("%s-jumpbox-osdisk", name)),
				Caching:      azcompute.CachingTypesReadWrite,
				CreateOption: pulumi.String("FromImage"),
				DeleteOption: pulumi.String("Delete"),
			},
		},
		NetworkProfile: &azcompute.NetworkProfileArgs{
			NetworkInterfaces: azcompute.NetworkInterfaceReferenceArray{
				&azcompute.NetworkInterfaceReferenceArgs{
					Id:      jumpboxNIC.ID(),
					Primary: pulumi.Bool(true),
				},
			},
		},
		OsProfile: &azcompute.OSProfileArgs{
			AdminUsername: pulumi.String("ptd-admin"),
			ComputerName:  pulumi.String(fmt.Sprintf("%s-jumpbox", name)),
			LinuxConfiguration: &azcompute.LinuxConfigurationArgs{
				DisablePasswordAuthentication: pulumi.Bool(true),
				PatchSettings: &azcompute.LinuxPatchSettingsArgs{
					PatchMode:      pulumi.String("AutomaticByPlatform"),
					AssessmentMode: pulumi.String("AutomaticByPlatform"),
				},
				Ssh: &azcompute.SshConfigurationArgs{
					PublicKeys: azcompute.SshPublicKeyTypeArray{
						&azcompute.SshPublicKeyTypeArgs{
							Path:    pulumi.String("/home/ptd-admin/.ssh/authorized_keys"),
							KeyData: sshKey.PublicKeyOpenssh,
						},
					},
				},
			},
		},
		Tags: jumpboxTags,
	},
		// VM is not protected (allow recreation on image version updates).
		pulumi.ReplaceOnChanges([]string{"storageProfile.imageReference.version"}),
		pulumi.DeleteBeforeReplace(true),
		withBastionAlias(),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("jumpbox vm: %w", err)
	}

	return bastionHost, jumpbox, sshKey, nil
}

// azureTagMergeName clones a pulumi.StringMap and sets a "Name" tag (Python merged
// tags | {"Name": ...} on bastion resources).
func azureTagMergeName(tags pulumi.StringMap, name string) pulumi.StringMap {
	out := pulumi.StringMap{}
	for k, v := range tags {
		out[k] = v
	}
	out["Name"] = pulumi.String(name)
	return out
}

// azureBuildMimirPassword mirrors _define_mimir_password: a RandomPassword (not
// protected) and a Key Vault secret holding it.
func azureBuildMimirPassword(
	ctx *pulumi.Context,
	params azureWorkloadPersistentParams,
	tags pulumi.StringMap,
	protect bool,
	withAlias func() pulumi.ResourceOption,
) (*random.RandomPassword, error) {
	cn := params.compoundName
	pw, err := random.NewRandomPassword(ctx, fmt.Sprintf("%s-mimir-auth", cn), &random.RandomPasswordArgs{
		Special:         pulumi.Bool(true),
		OverrideSpecial: pulumi.String("-/_"),
		Length:          pulumi.Int(36),
	}, pulumi.Protect(false), withAlias())
	if err != nil {
		return nil, fmt.Errorf("mimir password: %w", err)
	}

	if _, err := azkeyvault.NewSecret(ctx, fmt.Sprintf("%s-mimir-auth", cn), &azkeyvault.SecretArgs{
		SecretName:        pulumi.String(fmt.Sprintf("%s-mimir-auth", cn)),
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		Properties: &azkeyvault.SecretPropertiesArgs{
			Value: pw.Result,
		},
		VaultName: pulumi.String(params.keyVaultName),
		Tags:      tags,
	}, protectOpt(protect), withAlias()); err != nil {
		return nil, fmt.Errorf("mimir secret: %w", err)
	}

	return pw, nil
}
