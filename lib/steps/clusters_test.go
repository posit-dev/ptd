package steps

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClustersStepName(t *testing.T) {
	step := &ClustersStep{}
	assert.Equal(t, "clusters", step.Name())
}

func TestClustersStepProxyRequired(t *testing.T) {
	step := &ClustersStep{}
	assert.True(t, step.ProxyRequired())
}

func TestClustersStepNilTarget(t *testing.T) {
	step := &ClustersStep{}
	step.Set(nil, nil, StepOptions{})
	err := step.Run(context.Background())
	assert.ErrorContains(t, err, "clusters step requires a destination target")
}

// clustersMocks implements pulumi.MockResourceMonitor for clusters deploy tests.
type clustersMocks struct {
	mu        sync.Mutex
	resources []pulumi.MockResourceArgs
}

func (m *clustersMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources = append(m.resources, args)
	return args.Name + "_id", args.Inputs, nil
}

func (m *clustersMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

// clustersResourceNames extracts resource names from mock args.
func clustersResourceNames(resources []pulumi.MockResourceArgs) []string {
	names := make([]string, len(resources))
	for i, r := range resources {
		names[i] = r.Name
	}
	return names
}

// minimalAWSClustersParams builds a minimal awsClustersParams for testing.
func minimalAWSClustersParams(compoundName string, releases []string, siteNames []string) awsClustersParams {
	clusters := make(map[string]types.AWSWorkloadClusterConfig, len(releases))
	kubeconfigsByRelease := make(map[string]string, len(releases))
	for _, r := range releases {
		clusters[r] = types.AWSWorkloadClusterConfig{Spec: types.AWSWorkloadClusterSpec{
			ClusterOIDCIssuerURL: "https://oidc.eks.us-east-1.amazonaws.com/id/EXAMPLEID",
		}}
		kubeconfigsByRelease[r] = "apiVersion: v1\nkind: Config\n"
	}
	sites := make(map[string]types.SiteConfig, len(siteNames))
	for _, s := range siteNames {
		sites[s] = types.SiteConfig{Spec: types.SiteConfigSpec{Domain: s + ".example.com"}}
	}
	return awsClustersParams{
		compoundName:              compoundName,
		accountID:                 "123456789012",
		region:                    "us-east-1",
		iamPermissionsBoundaryARN: "arn:aws:iam::123456789012:policy/PositTeamDedicatedAdmin",
		teamOperatorPolicyName:    fmt.Sprintf("team-operator.%s.posit.team", compoundName),
		chronicleBucketName:       "chronicle-bucket-" + compoundName,
		ppmBucketName:             "ppm-bucket-" + compoundName,
		oidcURLTails:              []string{"oidc.eks.us-east-1.amazonaws.com/id/EXAMPLEID"},
		kubeconfigsByRelease:      kubeconfigsByRelease,
		clusters:                  clusters,
		sites:                     sites,
		resourceTags:              map[string]string{},
		networkTrust:              "FULL",
		keycloakEnabled:           false,
		externalDNSEnabled:        false,
		autoscalingEnabled:        false,
		tailscaleEnabled:          false,
		grafanaDBAddress:          "db.example.com",
		grafanaDBPW:               "grafana-pw",
	}
}

// minimalAzureClustersParams builds a minimal azureClustersParams for testing.
func minimalAzureClustersParams(compoundName string, releases []string) azureClustersParams {
	clusters := make(map[string]types.AzureWorkloadClusterConfig, len(releases))
	kubeconfigsByRelease := make(map[string]string, len(releases))
	for _, r := range releases {
		clusters[r] = types.AzureWorkloadClusterConfig{}
		kubeconfigsByRelease[r] = "apiVersion: v1\nkind: Config\n"
	}
	sanitized := compoundName
	for i := 0; i < len(sanitized); {
		if sanitized[i] == '-' {
			sanitized = sanitized[:i] + sanitized[i+1:]
		} else {
			i++
		}
	}
	if len(sanitized) > 14 {
		sanitized = sanitized[:14]
	}
	return azureClustersParams{
		compoundName:                 compoundName,
		subscriptionID:               "sub-12345",
		region:                       "eastus",
		resourceGroupName:            "rsg-ptd-" + compoundName,
		clusters:                     clusters,
		kubeconfigsByRelease:         kubeconfigsByRelease,
		dnsForwardDomains:            nil,
		resourceTags:                 map[string]string{},
		azureFilesStorageAccountName: "stptdfiles" + sanitized,
	}
}

// --- AWS deploy tests ---

func TestAWSClustersDeployOneReleaseOneSite(t *testing.T) {
	mocks := &clustersMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAWSClustersParams("myworkload", []string{"20250101"}, []string{"main"})
		return awsClustersDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-workload-clusters", "myworkload", mocks))

	require.NoError(t, err)

	names := clustersResourceNames(mocks.resources)

	// K8s provider
	assert.Contains(t, names, "myworkload-20250101-k8s")

	// IAM: home role
	assert.Contains(t, names, "home.20250101.myworkload.posit.team")

	// IAM: chronicle role and policies
	assert.Contains(t, names, "chr.20250101.main.myworkload.posit.team")
	assert.Contains(t, names, "chronicle-s3-bucket.20250101.main.myworkload.posit.team")
	assert.Contains(t, names, "chr-ro.20250101.main.myworkload.posit.team")
	assert.Contains(t, names, "chronicle-s3-bucket-read-only.20250101.main.myworkload.posit.team")

	// IAM: connect
	assert.Contains(t, names, "pub.20250101.myworkload.posit.team")
	assert.Contains(t, names, "pub-ses.20250101.main.myworkload.posit.team")

	// IAM: workbench
	assert.Contains(t, names, "dev.20250101.myworkload.posit.team")
	assert.Contains(t, names, "dev-ses.20250101.main.myworkload.posit.team")

	// IAM: ppm
	assert.Contains(t, names, "pkg.20250101.main.myworkload.posit.team")
	assert.Contains(t, names, "ppm-s3-bucket.20250101.main.myworkload.posit.team")

	// IAM: team operator
	assert.Contains(t, names, "team-operator.20250101.myworkload.posit.team")

	// K8s: grafana
	assert.Contains(t, names, "myworkload-20250101-grafana-ns")
	assert.Contains(t, names, "myworkload-20250101-grafana-db-url")

	// K8s: posit-team namespace (child of TeamOperator sub-component)
	assert.Contains(t, names, "myworkload-20250101-posit-team")

	// K8s: team-operator helm release
	assert.Contains(t, names, "myworkload-20250101-team-operator")

	// K8s: helm-controller namespace (child of HelmController sub-component)
	assert.Contains(t, names, "myworkload-20250101-helm-controller-namespace")

	// keycloak should NOT be present (disabled)
	assert.NotContains(t, names, "keycloak.20250101.myworkload.posit.team")

	// external-dns should NOT be present (disabled)
	assert.NotContains(t, names, "myworkload-20250101-external-dns")
}

func TestAWSClustersDeployOneReleaseTwoSites(t *testing.T) {
	mocks := &clustersMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAWSClustersParams("myworkload", []string{"20250101"}, []string{"beta", "main"})
		return awsClustersDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-workload-clusters", "myworkload", mocks))

	require.NoError(t, err)

	names := clustersResourceNames(mocks.resources)

	// Per-site roles should exist for both sites
	assert.Contains(t, names, "chr.20250101.beta.myworkload.posit.team")
	assert.Contains(t, names, "chr.20250101.main.myworkload.posit.team")
	assert.Contains(t, names, "pub-ses.20250101.beta.myworkload.posit.team")
	assert.Contains(t, names, "pub-ses.20250101.main.myworkload.posit.team")
	assert.Contains(t, names, "dev-ses.20250101.beta.myworkload.posit.team")
	assert.Contains(t, names, "dev-ses.20250101.main.myworkload.posit.team")
	assert.Contains(t, names, "pkg.20250101.beta.myworkload.posit.team")
	assert.Contains(t, names, "pkg.20250101.main.myworkload.posit.team")
}

func TestAWSClustersDeployTwoReleasesOneSite(t *testing.T) {
	mocks := &clustersMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAWSClustersParams("myworkload", []string{"20250101", "20250201"}, []string{"main"})
		return awsClustersDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-workload-clusters", "myworkload", mocks))

	require.NoError(t, err)

	names := clustersResourceNames(mocks.resources)

	// Both releases should have their own resources
	assert.Contains(t, names, "home.20250101.myworkload.posit.team")
	assert.Contains(t, names, "home.20250201.myworkload.posit.team")
	assert.Contains(t, names, "myworkload-20250101-team-operator")
	assert.Contains(t, names, "myworkload-20250201-team-operator")
	assert.Contains(t, names, "myworkload-20250101-grafana-ns")
	assert.Contains(t, names, "myworkload-20250201-grafana-ns")
}

func TestAWSClustersDeployKeycloakEnabled(t *testing.T) {
	mocks := &clustersMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAWSClustersParams("myworkload", []string{"20250101"}, []string{"main"})
		params.keycloakEnabled = true
		return awsClustersDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-workload-clusters", "myworkload", mocks))

	require.NoError(t, err)

	names := clustersResourceNames(mocks.resources)
	assert.Contains(t, names, "keycloak.20250101.myworkload.posit.team")
}

func TestAWSClustersDeployExternalDNSEnabled(t *testing.T) {
	mocks := &clustersMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAWSClustersParams("myworkload", []string{"20250101"}, []string{"main"})
		params.externalDNSEnabled = true
		return awsClustersDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-aws-workload-clusters", "myworkload", mocks))

	require.NoError(t, err)

	names := clustersResourceNames(mocks.resources)
	assert.Contains(t, names, "myworkload-20250101-external-dns")
}

// --- AWS IAM helper tests ---

func TestBuildIRSATrustPolicyNoOIDC(t *testing.T) {
	// With no OIDC providers, should fall back to account root principal
	policy := buildIRSATrustPolicy("posit-team", []string{"my-sa"}, "123456789012", nil, "us-east-1")

	var doc map[string]interface{}
	err := json.Unmarshal([]byte(policy), &doc)
	require.NoError(t, err)

	stmts := doc["Statement"].([]interface{})
	require.Len(t, stmts, 1)
	stmt := stmts[0].(map[string]interface{})
	principal := stmt["Principal"].(map[string]interface{})
	assert.Equal(t, "arn:aws:iam::123456789012:root", principal["AWS"])
}

func TestBuildIRSATrustPolicyWithOIDC(t *testing.T) {
	policy := buildIRSATrustPolicy(
		"posit-team",
		[]string{"my-sa"},
		"123456789012",
		[]string{"oidc.eks.us-east-1.amazonaws.com/id/ABC"},
		"us-east-1",
	)

	var doc map[string]interface{}
	err := json.Unmarshal([]byte(policy), &doc)
	require.NoError(t, err)

	stmts := doc["Statement"].([]interface{})
	// 1 statement per OIDC tail (all SAs grouped into one sub list)
	assert.Len(t, stmts, 1)

	oidcStmt := stmts[0].(map[string]interface{})
	assert.Equal(t, "sts:AssumeRoleWithWebIdentity", oidcStmt["Action"])
	principal := oidcStmt["Principal"].(map[string]interface{})
	assert.Equal(t, "arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/ABC", principal["Federated"])
}

func TestBuildIRSATrustPolicyMultipleSAs(t *testing.T) {
	policy := buildIRSATrustPolicy(
		"posit-team",
		[]string{"sa-one", "sa-two"},
		"123456789012",
		[]string{"oidc.eks.us-east-1.amazonaws.com/id/ABC"},
		"us-east-1",
	)

	var doc map[string]interface{}
	err := json.Unmarshal([]byte(policy), &doc)
	require.NoError(t, err)

	stmts := doc["Statement"].([]interface{})
	// 1 statement per OIDC tail; multiple SAs are combined into one sub list
	assert.Len(t, stmts, 1)

	// Both SAs appear as subs in the single statement
	cond := stmts[0].(map[string]interface{})["Condition"].(map[string]interface{})
	eq := cond["StringEquals"].(map[string]interface{})
	subs := eq["oidc.eks.us-east-1.amazonaws.com/id/ABC:sub"].([]interface{})
	assert.Len(t, subs, 2)
}

func TestBuildGrafanaDBURL(t *testing.T) {
	url := buildGrafanaDBURL("myworkload", "mypw", "db.example.com")
	// Decode and verify contents
	decoded, err := base64.StdEncoding.DecodeString(url)
	require.NoError(t, err)
	assert.Equal(t, "postgres://grafana-myworkload:mypw@db.example.com/grafana-myworkload", string(decoded))
}

// --- Azure deploy tests ---

func TestAzureClustersDeployOneRelease(t *testing.T) {
	mocks := &clustersMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAzureClustersParams("myworkload", []string{"20250101"})
		return azureClustersDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-azure-workload-clusters", "myworkload", mocks))

	require.NoError(t, err)

	names := clustersResourceNames(mocks.resources)

	// K8s provider (Azure logical name is compound-release)
	assert.Contains(t, names, "myworkload-20250101")

	// Team operator namespace and helm release
	assert.Contains(t, names, "myworkload-20250101-posit-team")
	assert.Contains(t, names, "myworkload-20250101-team-operator")

	// Helm controller namespace
	assert.Contains(t, names, "myworkload-20250101-helm-controller-namespace")
}

func TestAzureClustersDeployTwoReleases(t *testing.T) {
	mocks := &clustersMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := minimalAzureClustersParams("myworkload", []string{"20250101", "20250201"})
		return azureClustersDeploy(ctx, nil, params)
	}, pulumi.WithMocks("ptd-azure-workload-clusters", "myworkload", mocks))

	require.NoError(t, err)

	names := clustersResourceNames(mocks.resources)

	assert.Contains(t, names, "myworkload-20250101-team-operator")
	assert.Contains(t, names, "myworkload-20250201-team-operator")
	assert.Contains(t, names, "myworkload-20250101-helm-controller-namespace")
	assert.Contains(t, names, "myworkload-20250201-helm-controller-namespace")
}
