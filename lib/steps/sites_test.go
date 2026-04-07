package steps

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/posit-dev/ptd/lib/types"
)

func TestSitesStepName(t *testing.T) {
	step := &SitesStep{}
	assert.Equal(t, "sites", step.Name())
}

func TestSitesStepProxyRequired(t *testing.T) {
	step := &SitesStep{}
	assert.True(t, step.ProxyRequired())
}

// sitesMocks implements pulumi.MockResourceMonitor for testing the deploy functions.
type sitesMocks struct {
	resources []pulumi.MockResourceArgs
}

func (m *sitesMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.resources = append(m.resources, args)
	return args.Name + "_id", args.Inputs, nil
}

func (m *sitesMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

// sitesResourceNames extracts resource names from mock args.
func sitesResourceNames(resources []pulumi.MockResourceArgs) []string {
	names := make([]string, len(resources))
	for i, r := range resources {
		names[i] = r.Name
	}
	return names
}

// minimalAWSSiteParams builds a minimal awsSiteParams for testing.
func minimalAWSSiteParams(compoundName string, releases []string, siteNames []string) awsSiteParams {
	clusters := make(map[string]types.AWSWorkloadClusterConfig, len(releases))
	kubeconfigsByRelease := make(map[string]string, len(releases))
	for _, r := range releases {
		clusters[r] = types.AWSWorkloadClusterConfig{Spec: types.AWSWorkloadClusterSpec{}}
		kubeconfigsByRelease[r] = "apiVersion: v1\nkind: Config\n"
	}
	sites := make(map[string]types.SiteConfig, len(siteNames))
	for _, s := range siteNames {
		sites[s] = types.SiteConfig{Spec: types.SiteConfigSpec{Domain: s + ".example.com"}}
	}
	return awsSiteParams{
		compoundName:         compoundName,
		accountID:            "123456789012",
		region:               "us-east-1",
		chronicleBucket:      "chronicle-bucket",
		packageManagerBucket: "ppm-bucket",
		fsDnsName:            "fs.example.com",
		mainDBSecretARN:      "arn:aws:secretsmanager:us-east-1:123456789012:secret:db",
		networkTrust:         0,
		kubeconfigsByRelease: kubeconfigsByRelease,
		clusters:             clusters,
		sites:                sites,
		resourceTags:         map[string]string{},
	}
}

// --- AWS deploy tests ---

func TestAWSSitesDeployOneReleaseOneSite(t *testing.T) {
	mocks := &sitesMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAWSSiteParams("myworkload", []string{"20250101"}, []string{"main"})
		return awsSitesDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-workload-sites", "myworkload", mocks))

	require.NoError(t, err)

	// 1 kubernetes provider + 1 Site CRD = 2 resources
	assert.Len(t, mocks.resources, 2)
	names := sitesResourceNames(mocks.resources)
	assert.Contains(t, names, "myworkload-20250101-k8s")
	assert.Contains(t, names, "20250101-main")
}

func TestAWSSitesDeployTwoReleasesOneSite(t *testing.T) {
	mocks := &sitesMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAWSSiteParams("myworkload", []string{"20250101", "20250201"}, []string{"main"})
		return awsSitesDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-workload-sites", "myworkload", mocks))

	require.NoError(t, err)

	// 2 providers + 2 sites = 4 resources
	assert.Len(t, mocks.resources, 4)
	names := sitesResourceNames(mocks.resources)
	assert.Contains(t, names, "myworkload-20250101-k8s")
	assert.Contains(t, names, "myworkload-20250201-k8s")
	assert.Contains(t, names, "20250101-main")
	assert.Contains(t, names, "20250201-main")
}

func TestAWSSitesDeployOneReleaseTwoSites(t *testing.T) {
	mocks := &sitesMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAWSSiteParams("myworkload", []string{"20250101"}, []string{"beta", "main"})
		return awsSitesDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-workload-sites", "myworkload", mocks))

	require.NoError(t, err)

	// 1 provider + 2 sites = 3 resources
	assert.Len(t, mocks.resources, 3)
	names := sitesResourceNames(mocks.resources)
	assert.Contains(t, names, "myworkload-20250101-k8s")
	assert.Contains(t, names, "20250101-main")
	assert.Contains(t, names, "20250101-beta")
}

// --- AWS spec tests ---

func TestBuildAWSSiteSpecBasic(t *testing.T) {
	params := minimalAWSSiteParams("myworkload", []string{"20250101"}, []string{"main"})
	params.networkTrust = 50

	clusterCfg := types.AWSWorkloadClusterSpec{}
	siteConfig := types.SiteConfigSpec{Domain: "main.example.com"}

	spec := buildAWSSiteSpec(params, "20250101", "main", siteConfig, clusterCfg)

	assert.Equal(t, "123456789012", spec["awsAccountId"])
	assert.Equal(t, "20250101", spec["clusterDate"])
	assert.Equal(t, "main.example.com", spec["domain"])
	assert.Equal(t, 50, spec["networkTrust"])
	assert.Equal(t, "aws", spec["secretType"])
	assert.Equal(t, "myworkload", spec["workloadCompoundName"])

	chronicle := spec["chronicle"].(map[string]interface{})
	assert.Equal(t, "chronicle-bucket", chronicle["s3Bucket"])

	ppm := spec["packageManager"].(map[string]interface{})
	assert.Equal(t, "ppm-bucket", ppm["s3Bucket"])

	secret := spec["secret"].(map[string]interface{})
	assert.Equal(t, "aws", secret["type"])
	assert.Equal(t, "myworkload-main.posit.team", secret["vaultName"])

	workloadSecret := spec["workloadSecret"].(map[string]interface{})
	assert.Equal(t, "aws", workloadSecret["type"])
	assert.Equal(t, "myworkload.posit.team", workloadSecret["vaultName"])

	dbSecret := spec["mainDatabaseCredentialSecret"].(map[string]interface{})
	assert.Equal(t, "aws", dbSecret["type"])
	assert.Equal(t, "arn:aws:secretsmanager:us-east-1:123456789012:secret:db", dbSecret["vaultName"])

	volumeSource := spec["volumeSource"].(map[string]interface{})
	assert.Equal(t, "nfs", volumeSource["type"])
	assert.Equal(t, "fs.example.com", volumeSource["dnsName"])

	_, hasEfs := spec["efsEnabled"]
	assert.False(t, hasEfs)
	_, hasWorkbench := spec["workbench"]
	assert.False(t, hasWorkbench)
}

func TestBuildAWSSiteSpecEfsEnabledFlag(t *testing.T) {
	params := minimalAWSSiteParams("myworkload", []string{"20250101"}, []string{"main"})
	params.vpcCidr = "10.0.0.0/16"

	clusterCfg := types.AWSWorkloadClusterSpec{EnableEfsCsiDriver: true}
	spec := buildAWSSiteSpec(params, "20250101", "main", types.SiteConfigSpec{Domain: "d.example.com"}, clusterCfg)

	assert.Equal(t, true, spec["efsEnabled"])
	assert.Equal(t, "10.0.0.0/16", spec["vpcCIDR"])
}

func TestBuildAWSSiteSpecEfsConfigNotNil(t *testing.T) {
	params := minimalAWSSiteParams("myworkload", []string{"20250101"}, []string{"main"})
	params.vpcCidr = "10.0.0.0/16"

	clusterCfg := types.AWSWorkloadClusterSpec{
		EfsConfig: &types.EFSConfig{FileSystemID: "fs-abc123"},
	}
	spec := buildAWSSiteSpec(params, "20250101", "main", types.SiteConfigSpec{Domain: "d.example.com"}, clusterCfg)

	assert.Equal(t, true, spec["efsEnabled"])
	assert.Equal(t, "10.0.0.0/16", spec["vpcCIDR"])
}

func TestBuildAWSSiteSpecEfsNoVpcCidr(t *testing.T) {
	params := minimalAWSSiteParams("myworkload", []string{"20250101"}, []string{"main"})
	params.vpcCidr = ""

	clusterCfg := types.AWSWorkloadClusterSpec{EnableEfsCsiDriver: true}
	spec := buildAWSSiteSpec(params, "20250101", "main", types.SiteConfigSpec{Domain: "d.example.com"}, clusterCfg)

	assert.Equal(t, true, spec["efsEnabled"])
	_, hasVpcCidr := spec["vpcCIDR"]
	assert.False(t, hasVpcCidr, "vpcCIDR should not be set when vpcCidr is empty")
}

func TestBuildAWSSiteSpecSessionTolerations(t *testing.T) {
	params := minimalAWSSiteParams("myworkload", []string{"20250101"}, []string{"main"})

	clusterCfg := types.AWSWorkloadClusterSpec{
		KarpenterConfig: &types.KarpenterConfig{
			NodePools: []types.KarpenterNodePool{{SessionTaints: true}},
		},
	}
	spec := buildAWSSiteSpec(params, "20250101", "main", types.SiteConfigSpec{Domain: "d.example.com"}, clusterCfg)

	workbench := spec["workbench"].(map[string]interface{})
	tolerations := workbench["sessionTolerations"].([]map[string]interface{})
	require.Len(t, tolerations, 1)
	assert.Equal(t, "workload-type", tolerations[0]["key"])
	assert.Equal(t, "Equal", tolerations[0]["operator"])
	assert.Equal(t, "session", tolerations[0]["value"])
	assert.Equal(t, "NoSchedule", tolerations[0]["effect"])
}

// --- sessionTolerations unit tests ---

func TestSessionTolerations(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		assert.Nil(t, sessionTolerations(nil))
	})

	t.Run("no session taints", func(t *testing.T) {
		kc := &types.KarpenterConfig{
			NodePools: []types.KarpenterNodePool{{SessionTaints: false}},
		}
		assert.Nil(t, sessionTolerations(kc))
	})

	t.Run("session taints true", func(t *testing.T) {
		kc := &types.KarpenterConfig{
			NodePools: []types.KarpenterNodePool{{SessionTaints: true}},
		}
		tols := sessionTolerations(kc)
		require.Len(t, tols, 1)
		assert.Equal(t, "workload-type", tols[0]["key"])
		assert.Equal(t, "Equal", tols[0]["operator"])
		assert.Equal(t, "session", tols[0]["value"])
		assert.Equal(t, "NoSchedule", tols[0]["effect"])
	})

	t.Run("empty node pools", func(t *testing.T) {
		kc := &types.KarpenterConfig{NodePools: []types.KarpenterNodePool{}}
		assert.Nil(t, sessionTolerations(kc))
	})

	t.Run("first pool with session taints true returns toleration", func(t *testing.T) {
		kc := &types.KarpenterConfig{
			NodePools: []types.KarpenterNodePool{
				{SessionTaints: false},
				{SessionTaints: true},
			},
		}
		tols := sessionTolerations(kc)
		require.Len(t, tols, 1)
	})
}

// --- NetworkTrustValue tests ---

func TestNetworkTrustValue(t *testing.T) {
	assert.Equal(t, 100, types.NetworkTrustValue(""), "empty defaults to FULL")
	assert.Equal(t, 100, types.NetworkTrustValue("FULL"))
	assert.Equal(t, 0, types.NetworkTrustValue("ZERO"))
	assert.Equal(t, 50, types.NetworkTrustValue("SAMESITE"))
}

// --- Azure deploy tests ---

// minimalAzureSiteParams builds a minimal azureSiteParams for testing.
func minimalAzureSiteParams(compoundName string, releases []string, siteNames []string) azureSiteParams {
	clusters := make(map[string]types.AzureWorkloadClusterConfig, len(releases))
	kubeconfigsByRelease := make(map[string]string, len(releases))
	for _, r := range releases {
		clusters[r] = types.AzureWorkloadClusterConfig{}
		kubeconfigsByRelease[r] = "apiVersion: v1\nkind: Config\n"
	}
	sites := make(map[string]types.SiteConfig, len(siteNames))
	for _, s := range siteNames {
		sites[s] = types.SiteConfig{Spec: types.SiteConfigSpec{Domain: s + ".example.com"}}
	}
	return azureSiteParams{
		compoundName:         compoundName,
		networkTrust:         0,
		ppmFileShareSizeGib:  100,
		kubeconfigsByRelease: kubeconfigsByRelease,
		clusters:             clusters,
		sites:                sites,
		resourceTags:         map[string]string{},
	}
}

func TestAzureSitesDeployOneReleaseOneSite(t *testing.T) {
	mocks := &sitesMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAzureSiteParams("myworkload", []string{"20250101"}, []string{"main"})
		return azureSitesDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-azure-workload-sites", "myworkload", mocks))

	require.NoError(t, err)

	// 1 provider + 1 site = 2 resources
	assert.Len(t, mocks.resources, 2)
	names := sitesResourceNames(mocks.resources)
	assert.Contains(t, names, "myworkload-20250101-k8s")
	assert.Contains(t, names, "20250101-main")
}

func TestAzureSitesDeployTwoReleasesTwoSites(t *testing.T) {
	mocks := &sitesMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAzureSiteParams("myworkload", []string{"20250101", "20250201"}, []string{"beta", "main"})
		return azureSitesDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-azure-workload-sites", "myworkload", mocks))

	require.NoError(t, err)

	// 2 providers + 4 sites = 6 resources
	assert.Len(t, mocks.resources, 6)
	names := sitesResourceNames(mocks.resources)
	assert.Contains(t, names, "myworkload-20250101-k8s")
	assert.Contains(t, names, "myworkload-20250201-k8s")
	assert.Contains(t, names, "20250101-main")
	assert.Contains(t, names, "20250101-beta")
	assert.Contains(t, names, "20250201-main")
	assert.Contains(t, names, "20250201-beta")
}

// --- Azure spec tests ---

func TestBuildAzureSiteSpec(t *testing.T) {
	params := minimalAzureSiteParams("myworkload", []string{"20250101"}, []string{"main"})
	params.networkTrust = 100
	params.ppmFileShareSizeGib = 200

	siteConfig := types.SiteConfigSpec{Domain: "main.example.com"}
	spec := buildAzureSiteSpec(params, "20250101", "main", siteConfig)

	assert.Equal(t, "20250101", spec["clusterDate"])
	assert.Equal(t, "main.example.com", spec["domain"])
	assert.Equal(t, 100, spec["networkTrust"])
	assert.Equal(t, "kubernetes", spec["secretType"])
	assert.Equal(t, "myworkload", spec["workloadCompoundName"])

	secret := spec["secret"].(map[string]interface{})
	assert.Equal(t, "kubernetes", secret["type"])
	assert.Equal(t, "myworkload-main.posit.team", secret["vaultName"])

	workloadSecret := spec["workloadSecret"].(map[string]interface{})
	assert.Equal(t, "kubernetes", workloadSecret["type"])
	assert.Equal(t, "myworkload.posit.team", workloadSecret["vaultName"])

	ppm := spec["packageManager"].(map[string]interface{})
	azureFiles := ppm["azureFiles"].(map[string]interface{})
	assert.Equal(t, "myworkload-azure-files-csi", azureFiles["storageClassName"])
	assert.Equal(t, 200, azureFiles["shareSizeGiB"])

	volumeSource := spec["volumeSource"].(map[string]interface{})
	assert.Equal(t, "azure-netapp", volumeSource["type"])
}

// --- applySiteOverrides tests ---

// setupSiteOverrideDir creates a temp directory structure with a site YAML override file
// and sets viper's targets_config_dir to point at it.
func setupSiteOverrideDir(t *testing.T, workloadName, siteName, yamlContent string) {
	t.Helper()
	tmpDir := t.TempDir()
	siteDir := filepath.Join(tmpDir, "__work__", workloadName, "site_"+siteName)
	require.NoError(t, os.MkdirAll(siteDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(siteDir, "site.yaml"), []byte(yamlContent), 0644))
	viper.Set("targets_config_dir", tmpDir)
	t.Cleanup(func() { viper.Set("targets_config_dir", "") })
}

func TestApplySiteOverridesNoFile(t *testing.T) {
	tmpDir := t.TempDir()
	viper.Set("targets_config_dir", tmpDir)
	defer viper.Set("targets_config_dir", "")

	base := map[string]interface{}{
		"domain":      "example.com",
		"clusterDate": "20250101",
	}

	result, err := applySiteOverrides(base, "nonexistent-workload", "main")
	require.NoError(t, err)
	assert.Equal(t, "example.com", result["domain"])
	assert.Equal(t, "20250101", result["clusterDate"])
}

func TestApplySiteOverridesSimpleMerge(t *testing.T) {
	siteYAML := `
apiVersion: core.posit.team/v1beta1
spec:
  workbench:
    replicas: 3
    image: custom-image:latest
`
	setupSiteOverrideDir(t, "myworkload", "main", siteYAML)

	base := map[string]interface{}{
		"domain":      "example.com",
		"clusterDate": "20250101",
	}

	result, err := applySiteOverrides(base, "myworkload", "main")
	require.NoError(t, err)

	// Original fields preserved.
	assert.Equal(t, "example.com", result["domain"])
	assert.Equal(t, "20250101", result["clusterDate"])

	// Override fields merged in.
	workbench := result["workbench"].(map[string]interface{})
	assert.Equal(t, 3, workbench["replicas"])
	assert.Equal(t, "custom-image:latest", workbench["image"])
}

func TestApplySiteOverridesDeepMerge(t *testing.T) {
	siteYAML := `
apiVersion: core.posit.team/v1beta1
spec:
  workbench:
    replicas: 5
`
	setupSiteOverrideDir(t, "myworkload", "main", siteYAML)

	base := map[string]interface{}{
		"domain": "example.com",
		"workbench": map[string]interface{}{
			"sessionTolerations": []map[string]interface{}{
				{"key": "workload-type", "operator": "Equal", "value": "session", "effect": "NoSchedule"},
			},
		},
	}

	result, err := applySiteOverrides(base, "myworkload", "main")
	require.NoError(t, err)

	workbench := result["workbench"].(map[string]interface{})
	// Override value merged.
	assert.Equal(t, 5, workbench["replicas"])
	// Existing value preserved.
	assert.NotNil(t, workbench["sessionTolerations"])
}

func TestApplySiteOverridesOverrideWins(t *testing.T) {
	siteYAML := `
apiVersion: core.posit.team/v1beta1
spec:
  domain: overridden.example.com
`
	setupSiteOverrideDir(t, "myworkload", "main", siteYAML)

	base := map[string]interface{}{
		"domain": "original.example.com",
	}

	result, err := applySiteOverrides(base, "myworkload", "main")
	require.NoError(t, err)
	assert.Equal(t, "overridden.example.com", result["domain"])
}

func TestApplySiteOverridesNoSpec(t *testing.T) {
	siteYAML := `
apiVersion: core.posit.team/v1beta1
`
	setupSiteOverrideDir(t, "myworkload", "main", siteYAML)

	base := map[string]interface{}{
		"domain": "example.com",
	}

	result, err := applySiteOverrides(base, "myworkload", "main")
	require.NoError(t, err)
	assert.Equal(t, "example.com", result["domain"])
}

// --- yamlMapToStringMap tests ---

func TestApplySiteOverridesPreservesBaseKeysInNestedMaps(t *testing.T) {
	// Base has workbench.sessionTolerations, override has workbench.replicas
	// — both should survive the deep merge.
	siteYAML := `
apiVersion: core.posit.team/v1beta1
spec:
  workbench:
    replicas: 2
    image: custom:latest
`
	setupSiteOverrideDir(t, "testworkload", "main", siteYAML)

	base := map[string]interface{}{
		"domain": "example.com",
		"workbench": map[string]interface{}{
			"sessionTolerations": []map[string]interface{}{
				{"key": "workload-type", "operator": "Equal", "value": "session", "effect": "NoSchedule"},
			},
		},
	}

	result, err := applySiteOverrides(base, "testworkload", "main")
	require.NoError(t, err)

	workbench := result["workbench"].(map[string]interface{})
	assert.Equal(t, 2, workbench["replicas"], "override field should be present")
	assert.Equal(t, "custom:latest", workbench["image"], "override field should be present")
	assert.NotNil(t, workbench["sessionTolerations"], "base field should be preserved")
}

func TestYamlMapToStringMapNested(t *testing.T) {
	input := map[interface{}]interface{}{
		"top": map[interface{}]interface{}{
			"nested": "value",
			"deep": map[interface{}]interface{}{
				"leaf": 42,
			},
		},
		"list": []interface{}{
			map[interface{}]interface{}{"a": 1},
			"plain",
		},
	}

	result := yamlMapToStringMap(input)

	top := result["top"].(map[string]interface{})
	assert.Equal(t, "value", top["nested"])
	deep := top["deep"].(map[string]interface{})
	assert.Equal(t, 42, deep["leaf"])

	list := result["list"].([]interface{})
	listMap := list[0].(map[string]interface{})
	assert.Equal(t, 1, listMap["a"])
	assert.Equal(t, "plain", list[1])
}
