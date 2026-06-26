package steps

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/posit-dev/ptd/lib/types/typestest"
)

// persistentMocks records created resources and stubs the Pulumi data sources
// the AWS workload persistent deploy invokes.
type persistentMocks struct {
	mu        sync.Mutex
	resources []pulumi.MockResourceArgs
}

func (m *persistentMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources = append(m.resources, args)
	return args.Name + "_id", args.Inputs, nil
}

func (m *persistentMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	switch args.Token {
	case "aws:index/getAvailabilityZones:getAvailabilityZones":
		return resource.PropertyMap{
			"zoneIds": resource.NewArrayProperty([]resource.PropertyValue{
				resource.NewStringProperty("use2-az1"),
				resource.NewStringProperty("use2-az2"),
				resource.NewStringProperty("use2-az3"),
			}),
		}, nil
	case "aws:ec2/getVpcEndpointService:getVpcEndpointService":
		return resource.PropertyMap{
			"serviceName": resource.NewStringProperty("com.amazonaws.us-east-2.svc"),
			"serviceType": resource.NewStringProperty("Interface"),
		}, nil
	case "aws:iam/getPolicyDocument:getPolicyDocument":
		return resource.PropertyMap{
			"json": resource.NewStringProperty(`{"Version":"2012-10-17"}`),
		}, nil
	case "aws:rds/getInstance:getInstance":
		return resource.PropertyMap{
			"masterUserSecrets": resource.NewArrayProperty([]resource.PropertyValue{
				resource.NewObjectProperty(resource.PropertyMap{
					"secretArn": resource.NewStringProperty("arn:aws:secretsmanager:us-east-2:123:secret:rds!db-x"),
				}),
			}),
		}, nil
	}
	return resource.PropertyMap{}, nil
}

func (m *persistentMocks) names() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for _, r := range m.resources {
		out[r.Name] = true
	}
	return out
}

func mockAWSWorkloadTarget(name string) *typestest.MockTarget {
	tgt := &typestest.MockTarget{}
	tgt.On("Name").Return(name)
	tgt.On("CloudProvider").Return(types.AWS)
	tgt.On("ControlRoom").Return(false)
	tgt.On("Type").Return(types.TargetTypeWorkload)
	return tgt
}

func baseAWSWorkloadPersistentParams() awsWorkloadPersistentParams {
	cn := "demo01-staging"
	return awsWorkloadPersistentParams{
		compoundName:        cn,
		prefix:              "ptd",
		accountID:           "123456789012",
		callerARN:           "arn:aws:sts::123456789012:assumed-role/admin/x",
		region:              "us-east-2",
		environment:         "staging",
		vpcCIDR:             "10.42.0.0/16",
		iamPermissionsBound: "arn:aws:iam::123456789012:policy/PositTeamDedicatedAdmin",
		oidcURLTails:        []string{"oidc.eks.us-east-2.amazonaws.com/id/ABCDEF"},
		requiredTags: map[string]string{
			"posit.team/true-name":   "demo01",
			"posit.team/environment": "staging",
			"posit.team/managed-by":  persistentManagedByValue,
		},
		cfg: types.AWSWorkloadConfig{
			AccountID:                  "123456789012",
			Region:                     "us-east-2",
			VpcAzCount:                 3,
			BastionInstanceType:        "t4g.nano",
			DBAllocatedStorage:         100,
			DBEngineVersion:            "15.18",
			DBInstanceClass:            "db.t3.small",
			ProtectPersistentResources: true,
			Sites: map[string]types.SiteConfig{
				"main": {Spec: types.SiteConfigSpec{Domain: "demo01.example.com"}},
			},
			Clusters: map[string]types.AWSWorkloadClusterConfig{},
		},
		teamOperatorPolicyName:              "team-operator." + cn + ".posit.team",
		fsxOpenzfsRoleName:                  "aws-fsx-openzfs-csi-driver." + cn + ".posit.team",
		fsxNfsSGName:                        "fsx-nfs." + cn + ".posit.team",
		lbcRoleName:                         "aws-load-balancer-controller." + cn + ".posit.team",
		lbcPolicyName:                       "lbc." + cn + ".posit.team",
		externalDNSRoleName:                 "external-dns." + cn + ".posit.team",
		dnsUpdatePolicyName:                 "dns-update." + cn + ".posit.team",
		traefikForwardAuthRoleName:          "traefik-forward-auth." + cn + ".posit.team",
		traefikForwardAuthReadSecretsPolicy: "traefik-forward-auth-read-secrets." + cn + ".posit.team",
		mimirRoleName:                       "mimir." + cn + ".posit.team",
		mimirS3BucketName:                   cn + "-mimir",
		mimirS3BucketPolicyName:             "mimir-s3-bucket." + cn + ".posit.team",
		lokiRoleName:                        "loki." + cn + ".posit.team",
		lokiS3BucketName:                    cn + "-loki",
		lokiS3BucketPolicyName:              "loki-s3-bucket." + cn + ".posit.team",
		ebsCsiRoleName:                      "aws-ebs-csi." + cn + ".posit.team",
		alloyRoleName:                       "alloy." + cn + ".posit.team",
		alloyPolicyName:                     "alloy." + cn + ".posit.team",
	}
}

func TestAWSWorkloadPersistentDeploy_GreenfieldMultiAZ(t *testing.T) {
	mocks := &persistentMocks{}
	params := baseAWSWorkloadPersistentParams()

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		tgt := mockAWSWorkloadTarget(params.compoundName)
		return awsWorkloadPersistentDeploy(ctx, tgt, params)
	}, pulumi.WithMocks("ptd-aws-workload-persistent", "demo01-staging", mocks))

	require.NoError(t, err)

	names := mocks.names()
	cn := params.compoundName

	// Core persistent resources by logical name (verbatim from Python).
	for _, want := range []string{
		cn, // VPC + RDS instance + single-AZ FSx (multi-AZ uses "<cn>-filesystem")
		cn + "-allow-postgresql-traffic-vpc",
		cn + "-main-database-subnet-group",
		cn + "-main-database-parameter-group",
		cn + "-ppm-bucket",
		cn + "-chronicle-bucket",
		// named buckets use logical name "<cn>-<bucketName>-bucket" where bucketName
		// is itself "<cn>-mimir"/"<cn>-loki" (matches Python _define_named_bucket).
		cn + "-" + cn + "-mimir-bucket",
		cn + "-" + cn + "-loki-bucket",
		cn + "-mimir",
		params.teamOperatorPolicyName,
		params.lbcRoleName,
		params.lbcPolicyName,
		params.externalDNSRoleName,
		params.traefikForwardAuthRoleName,
		params.mimirRoleName,
		params.lokiRoleName,
		params.ebsCsiRoleName,
		params.alloyRoleName,
		params.fsxNfsSGName,
		"eks-nodes-fsx-nfs.posit.team",
		"aws-fsx-openzfs-csi-driver.posit.team",
		cn + "-filesystem", // multi-AZ default → MULTI_AZ_1
		cn + "-bastion",
	} {
		assert.Truef(t, names[want], "expected resource with logical name %q to be created", want)
	}
}

func TestAWSWorkloadPersistentDeploy_TailscaleNoBastion(t *testing.T) {
	mocks := &persistentMocks{}
	params := baseAWSWorkloadPersistentParams()
	params.cfg.TailscaleEnabled = true

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		tgt := mockAWSWorkloadTarget(params.compoundName)
		return awsWorkloadPersistentDeploy(ctx, tgt, params)
	}, pulumi.WithMocks("ptd-aws-workload-persistent", "demo01-staging", mocks))

	require.NoError(t, err)

	names := mocks.names()
	cn := params.compoundName
	// Tailscale subnet router resources created; no bastion instance.
	assert.True(t, names[cn+"-tailscale-fargate"], "expected tailscale ECS cluster")
	assert.True(t, names[cn+"-tailscale-task"], "expected tailscale task definition")
	assert.False(t, names[cn+"-bastion"], "bastion must not be created when tailscale is enabled")
}

// NOTE: mockAWSControlRoomTarget is defined in workspaces_test.go (same package).

func baseAWSControlRoomPersistentParams() awsControlRoomPersistentParams {
	cn := "ctrl01-production"
	return awsControlRoomPersistentParams{
		compoundName:        cn,
		accountID:           "123456789012",
		region:              "us-east-2",
		vpcCIDR:             "10.99.0.0/16",
		iamPermissionsBound: "arn:aws:iam::123456789012:policy/PositTeamDedicatedAdmin",
		requiredTags: map[string]string{
			"posit.team/true-name":   "ctrl01",
			"posit.team/environment": "production",
			"posit.team/managed-by":  persistentControlRoomManagedByValue,
		},
		cfg: types.AWSControlRoomConfig{
			AccountID:                  "123456789012",
			Region:                     "us-east-2",
			DBAllocatedStorage:         100,
			DBEngineVersion:            "16.14",
			DBInstanceClass:            "db.t3.small",
			TailscaleEnabled:           true,
			ProtectPersistentResources: true,
		},
	}
}

func TestAWSControlRoomPersistentDeploy(t *testing.T) {
	mocks := &persistentMocks{}
	params := baseAWSControlRoomPersistentParams()

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		tgt := mockAWSControlRoomTarget(params.compoundName)
		return awsControlRoomPersistentDeploy(ctx, tgt, params)
	}, pulumi.WithMocks("ptd-aws-control-room-persistent", "ctrl01-production", mocks))

	require.NoError(t, err)

	names := mocks.names()
	cn := params.compoundName

	// Core control-room persistent resources by logical name (verbatim from Python).
	for _, want := range []string{
		cn, // VPC + RDS instance
		cn + "-allow-postgresql-traffic-vpc",
		cn + "-main-database-subnet-group",
		cn + "-main-database-parameter-group",
		cn + "-releases",
		cn + "-releases-public-access-block",
		cn + "-releases-versioning",
		// Tailscale is always created for the control room.
		cn + "-tailscale-fargate",
		cn + "-tailscale-task",
		cn + "-tailscale-service",
	} {
		assert.Truef(t, names[want], "expected resource with logical name %q to be created", want)
	}

	// No bastion in the control room.
	assert.False(t, names[cn+"-bastion"], "control room must not create a bastion")
}

func TestAWSControlRoomPersistentDeploy_Postgres16ParameterGroup(t *testing.T) {
	mocks := &persistentMocks{}
	params := baseAWSControlRoomPersistentParams()

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		tgt := mockAWSControlRoomTarget(params.compoundName)
		return awsControlRoomPersistentDeploy(ctx, tgt, params)
	}, pulumi.WithMocks("ptd-aws-control-room-persistent", "ctrl01-production", mocks))

	require.NoError(t, err)

	// The parameter group must use the postgres16 family (control room default
	// engine 16.14), NOT postgres15.
	mocks.mu.Lock()
	defer mocks.mu.Unlock()
	var found bool
	for _, r := range mocks.resources {
		if r.TypeToken == "aws:rds/parameterGroup:ParameterGroup" {
			found = true
			fam := r.Inputs["family"]
			assert.True(t, fam.IsString(), "parameter group family must be a string")
			assert.Equal(t, "postgres16", fam.StringValue(), "control room parameter group family must be postgres16")
		}
	}
	assert.True(t, found, "expected an RDS parameter group to be created")
}

// --- Azure workload persistent ---

// azurePersistentMocks records created resources and stubs the Pulumi data
// sources the Azure workload persistent deploy invokes.
type azurePersistentMocks struct {
	mu        sync.Mutex
	resources []pulumi.MockResourceArgs
}

func (m *azurePersistentMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources = append(m.resources, args)
	return args.Name + "_id", args.Inputs, nil
}

func (m *azurePersistentMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	switch args.Token {
	case "azure-native:network:getVirtualNetwork":
		return resource.PropertyMap{
			"id": resource.NewStringProperty("/subscriptions/sub/resourceGroups/rsg-ptd-demo01-staging/providers/Microsoft.Network/virtualNetworks/existing-vnet"),
		}, nil
	}
	return resource.PropertyMap{}, nil
}

func (m *azurePersistentMocks) names() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for _, r := range m.resources {
		out[r.Name] = true
	}
	return out
}

func (m *azurePersistentMocks) typeCount(typeToken string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.resources {
		if r.TypeToken == typeToken {
			n++
		}
	}
	return n
}

func baseAzureWorkloadPersistentParams() azureWorkloadPersistentParams {
	cn := "demo01-staging"
	return azureWorkloadPersistentParams{
		compoundName:       cn,
		region:             "eastus",
		subscriptionID:     "sub",
		resourceGroupName:  "rsg-ptd-" + cn,
		keyVaultName:       "kv-ptd-demo01-staging",
		storageAccountName: "stptddemo01staging",
		requiredTags: map[string]string{
			"posit.team/true-name":   "demo01",
			"posit.team/environment": "staging",
			"posit.team/managed-by":  persistentAzureManagedByValue,
		},
		vnetRsgName:              "rsg-ptd-" + cn,
		vnetName:                 "vnet-ptd-" + cn,
		netappAccountName:        "naa-ptd-" + cn,
		netappPoolName:           "nap-ptd-" + cn,
		netappSnapshotPolicyName: "snp-ptd-" + cn,
		netappBackupVaultName:    "bkv-ptd-" + cn,
		netappBackupPolicyName:   "bkp-ptd-" + cn,
		netappSubnetName:         "snet-ptd-" + cn + "-netapp",
		appGatewaySubnetName:     "snet-ptd-" + cn + "-agw",
		acrRegistry:              azureACRRegistryName(cn),
		filesStorageAccountName:  azureFilesStorageAccountName(cn),
		bastionImageVersion:      "22.04.202412100",
		cfg: types.AzureWorkloadConfig{
			Region:                     "eastus",
			SubscriptionID:             "sub",
			ProtectPersistentResources: true,
			BastionInstanceType:        "Standard_B1s",
			NetappBackupRetentionDays:  30,
			NetappDailyBackupStartTime: "02:00",
			NetappSnapshotsToKeep:      7,
			Network: types.NetworkConfig{
				VnetCidr:             "10.0.0.0/16",
				PrivateSubnetCidr:    "10.0.1.0/24",
				DbSubnetCidr:         "10.0.2.0/24",
				NetAppSubnetCidr:     "10.0.3.0/24",
				AppGatewaySubnetCidr: "10.0.4.0/24",
				BastionSubnetCidr:    "10.0.5.0/24",
			},
			Sites: map[string]types.SiteConfig{
				"main": {Spec: types.SiteConfigSpec{Domain: "demo01.example.com"}},
			},
			Clusters: map[string]types.AzureWorkloadClusterConfig{},
		},
	}
}

func TestAzureWorkloadPersistentDeploy_NewVNet(t *testing.T) {
	mocks := &azurePersistentMocks{}
	params := baseAzureWorkloadPersistentParams()

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		tgt := mockAWSWorkloadTarget(params.compoundName) // deploy ignores target
		return azureWorkloadPersistentDeploy(ctx, tgt, params)
	}, pulumi.WithMocks("ptd-azure-workload-persistent", "demo01-staging", mocks))

	require.NoError(t, err)

	names := mocks.names()
	cn := params.compoundName

	for _, want := range []string{
		params.vnetName, // VirtualNetwork created (vnet_cidr set)
		"nsg-ptd-" + cn + "-private",
		"snet-ptd-" + cn + "-private",
		"nsg-ptd-" + cn + "-db",
		"snet-ptd-" + cn + "-db",
		"nsg-ptd-" + cn + "-netapp",
		params.netappSubnetName,
		"nsg-ptd-" + cn + "-app-gateway",
		params.appGatewaySubnetName,
		"nsg-ptd-" + cn + "-bastion",
		"AzureBastionSubnet",
		// NetApp base
		params.netappAccountName,
		params.netappPoolName,
		params.netappSnapshotPolicyName,
		params.netappBackupVaultName,
		params.netappBackupPolicyName,
		// Postgres
		cn + "-db-pw",
		"psql-ptd-" + cn,
		cn + "-postgres-admin-secret",
		"psql-ptd-" + cn + "-grafana",
		cn + "-grafana-postgres-admin-secret",
		// ACR + blobs + files
		params.acrRegistry,
		cn + "-chronicle-container",
		cn + "-loki-container",
		cn + "-files-storage",
		cn + "-files-pe",
		cn + "-files-dns-zone-group",
		// DNS (per-site, no root_domain)
		cn + "-main-dns-zone",
		// Bastion
		"ssh-key",
		"bas-ptd-" + cn + "-bastion-pip",
		"bas-ptd-" + cn + "-bastion-host",
		"bas-ptd-" + cn + "-bastion-jumpbox-nic",
		"bas-ptd-" + cn + "-bastion-jumpbox",
		// Mimir
		cn + "-mimir-auth",
	} {
		assert.Truef(t, names[want], "expected resource with logical name %q to be created", want)
	}

	// A new VNet must be created in the vnet_cidr branch.
	assert.Equal(t, 1, mocks.typeCount("azure-native:network:VirtualNetwork"), "exactly one VNet created")
	// 6 subnets (public branch off): private, db, netapp, app-gateway, bastion = 5.
	assert.Equal(t, 5, mocks.typeCount("azure-native:network:Subnet"), "5 subnets when no public subnet")
	// No netapp volumes without automated_volume_provisioning.
	assert.Equal(t, 0, mocks.typeCount("azure-native:netapp:CapacityPoolVolume"), "no volumes without automated provisioning")
}

func TestAzureWorkloadPersistentDeploy_ExistingVNet(t *testing.T) {
	mocks := &azurePersistentMocks{}
	params := baseAzureWorkloadPersistentParams()
	params.cfg.Network.ProvisionedVnetName = "existing-vnet"
	params.cfg.Network.VnetCidr = ""
	params.vnetName = "existing-vnet"

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		tgt := mockAWSWorkloadTarget(params.compoundName)
		return azureWorkloadPersistentDeploy(ctx, tgt, params)
	}, pulumi.WithMocks("ptd-azure-workload-persistent", "demo01-staging", mocks))

	require.NoError(t, err)

	// Adopting an existing VNet → no VirtualNetwork resource created.
	assert.Equal(t, 0, mocks.typeCount("azure-native:network:VirtualNetwork"), "no VNet created when adopting existing")
	// Subnets are still created (5).
	assert.Equal(t, 5, mocks.typeCount("azure-native:network:Subnet"))
}

func TestAzureWorkloadPersistentDeploy_PublicSubnet(t *testing.T) {
	mocks := &azurePersistentMocks{}
	params := baseAzureWorkloadPersistentParams()
	params.cfg.Network.PublicSubnetCidr = "10.0.6.0/24"

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		tgt := mockAWSWorkloadTarget(params.compoundName)
		return azureWorkloadPersistentDeploy(ctx, tgt, params)
	}, pulumi.WithMocks("ptd-azure-workload-persistent", "demo01-staging", mocks))

	require.NoError(t, err)

	names := mocks.names()
	cn := params.compoundName
	assert.True(t, names["pip-ptd-"+cn], "public ip created in public-subnet branch")
	assert.True(t, names["ng-ptd-"+cn], "nat gateway created in public-subnet branch")
	assert.True(t, names["snet-ptd-"+cn+"-public"], "public subnet created")
	// Now 6 subnets (5 + public).
	assert.Equal(t, 6, mocks.typeCount("azure-native:network:Subnet"))
}

func TestAzureWorkloadPersistentDeploy_AutomatedVolumeProvisioning(t *testing.T) {
	mocks := &azurePersistentMocks{}
	params := baseAzureWorkloadPersistentParams()
	params.cfg.AutomatedVolumeProvisioning = true

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		tgt := mockAWSWorkloadTarget(params.compoundName)
		return azureWorkloadPersistentDeploy(ctx, tgt, params)
	}, pulumi.WithMocks("ptd-azure-workload-persistent", "demo01-staging", mocks))

	require.NoError(t, err)

	names := mocks.names()
	// One site × 3 products = 3 volumes.
	assert.Equal(t, 3, mocks.typeCount("azure-native:netapp:CapacityPoolVolume"), "3 volumes for one site × 3 products")
	assert.True(t, names["nav-ptd-main-connect"])
	assert.True(t, names["nav-ptd-main-workbench"])
	assert.True(t, names["nav-ptd-main-workbench-shared"])
}

func TestAzureWorkloadPersistentDeploy_RootDomainSingleZone(t *testing.T) {
	mocks := &azurePersistentMocks{}
	params := baseAzureWorkloadPersistentParams()
	root := "example.com"
	params.cfg.RootDomain = &root
	params.cfg.Sites = map[string]types.SiteConfig{
		"main": {Spec: types.SiteConfigSpec{Domain: "demo01.example.com"}},
		"dev":  {Spec: types.SiteConfigSpec{Domain: "dev.example.com"}},
	}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		tgt := mockAWSWorkloadTarget(params.compoundName)
		return azureWorkloadPersistentDeploy(ctx, tgt, params)
	}, pulumi.WithMocks("ptd-azure-workload-persistent", "demo01-staging", mocks))

	require.NoError(t, err)

	names := mocks.names()
	cn := params.compoundName
	// root_domain set → single zone, named "<cn>-dns-zone" (no per-site zones).
	assert.True(t, names[cn+"-dns-zone"], "single root dns zone")
	assert.False(t, names[cn+"-main-dns-zone"], "no per-site zone when root_domain set")
	assert.Equal(t, 1, mocks.typeCount("azure-native:dns:Zone"), "exactly one DNS zone for root_domain")
}

func TestAzureWorkloadPersistentDeploy_PerSiteZones(t *testing.T) {
	mocks := &azurePersistentMocks{}
	params := baseAzureWorkloadPersistentParams()
	params.cfg.Sites = map[string]types.SiteConfig{
		"main": {Spec: types.SiteConfigSpec{Domain: "demo01.example.com"}},
		"dev":  {Spec: types.SiteConfigSpec{Domain: "dev.example.com"}},
	}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		tgt := mockAWSWorkloadTarget(params.compoundName)
		return azureWorkloadPersistentDeploy(ctx, tgt, params)
	}, pulumi.WithMocks("ptd-azure-workload-persistent", "demo01-staging", mocks))

	require.NoError(t, err)

	names := mocks.names()
	cn := params.compoundName
	assert.True(t, names[cn+"-main-dns-zone"])
	assert.True(t, names[cn+"-dev-dns-zone"])
	assert.Equal(t, 2, mocks.typeCount("azure-native:dns:Zone"), "one zone per site")
}

func TestAzureTagMapFormatsKeys(t *testing.T) {
	// azureTagMap must apply azure.FormatTagKey ('/' -> ':') so that child-resource
	// tags match the tags applied to the resource group at creation time (which run
	// through the same helper); otherwise every Azure resource's tags would churn.
	out := azureTagMap(map[string]string{
		"posit.team/environment": "staging",
		"CostCenter":             "rnd",
	})
	assert.Equal(t, pulumi.String("staging"), out["posit.team:environment"])
	assert.Equal(t, pulumi.String("rnd"), out["CostCenter"])
	// The unformatted key must not be present.
	_, hasUnformatted := out["posit.team/environment"]
	assert.False(t, hasUnformatted, "tag key with '/' must be reformatted to ':'")
}

func TestAzureNamingHelpers(t *testing.T) {
	// acr_registry: "crptd" + parts[0] + lower(parts[1:]).
	assert.Equal(t, "crptddemo01staging", azureACRRegistryName("demo01-staging"))
	// azure_files_storage_account_name: "stptdfiles" + nohyphens.lower()[:14].
	assert.Equal(t, "stptdfilesdemo01staging", azureFilesStorageAccountName("demo01-staging"))
	// truncation at 14 chars: a long no-hyphen name is cut.
	assert.Equal(t, "stptdfilesabcdefghijklmn", azureFilesStorageAccountName("abcdefghijklmnop"))
}

func TestAWSWorkloadPersistentDeploy_SingleAZFSx(t *testing.T) {
	mocks := &persistentMocks{}
	params := baseAWSWorkloadPersistentParams()
	multiAZ := false
	params.cfg.FsxOpenzfsMultiAz = &multiAZ

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		tgt := mockAWSWorkloadTarget(params.compoundName)
		return awsWorkloadPersistentDeploy(ctx, tgt, params)
	}, pulumi.WithMocks("ptd-aws-workload-persistent", "demo01-staging", mocks))

	require.NoError(t, err)

	names := mocks.names()
	cn := params.compoundName
	// Single-AZ FSx uses the bare "<cn>" logical name (not "<cn>-filesystem").
	assert.False(t, names[cn+"-filesystem"], "single-AZ must not create the multi-AZ <cn>-filesystem")
}
