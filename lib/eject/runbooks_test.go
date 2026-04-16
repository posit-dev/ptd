package eject

import (
	"strings"
	"testing"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func awsRunbookData() *RunbookData {
	return &RunbookData{
		WorkloadName: "acme-prod",
		Cloud:        "aws",
		Region:       "us-east-1",
		ClusterName:  "default_acme-prod-control-plane",
		Sites: []SiteData{
			{Name: "main", Domain: "connect.acme.com"},
			{Name: "secondary", Domain: "dev.acme.com"},
		},
	}
}

func azureRunbookData() *RunbookData {
	return &RunbookData{
		WorkloadName:  "contoso-staging",
		Cloud:         "azure",
		Region:        "eastus",
		ClusterName:   "aks-ptd-contoso",
		ResourceGroup: "rsg-ptd-contoso-staging",
		Sites: []SiteData{
			{Name: "main", Domain: "connect.contoso.com"},
		},
	}
}

func TestGenerateRunbooks_AWS_ReturnsExpectedFiles(t *testing.T) {
	results, err := GenerateRunbooks(awsRunbookData())

	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Contains(t, results, "day-to-day-ops.md")
	assert.Contains(t, results, "disaster-recovery.md")
}

func TestGenerateRunbooks_Azure_ReturnsExpectedFiles(t *testing.T) {
	results, err := GenerateRunbooks(azureRunbookData())

	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Contains(t, results, "day-to-day-ops.md")
	assert.Contains(t, results, "disaster-recovery.md")
}

func TestRunbook_DayToDayOps_AWS_ContainsSections(t *testing.T) {
	results, err := GenerateRunbooks(awsRunbookData())
	require.NoError(t, err)

	ops := results["day-to-day-ops.md"]

	assert.Contains(t, ops, "# Day-to-Day Operations — acme-prod")
	assert.Contains(t, ops, "## Running PTD Ensure Steps")
	assert.Contains(t, ops, "## Scaling Product Replicas")
	assert.Contains(t, ops, "## Updating Product Versions")
	assert.Contains(t, ops, "## Rotating TLS Certificates")
	assert.Contains(t, ops, "## Rotating Secrets")
	assert.Contains(t, ops, "## Checking Workload Health")
	assert.Contains(t, ops, "## Restarting Products")
}

func TestRunbook_DayToDayOps_AWS_Content(t *testing.T) {
	results, err := GenerateRunbooks(awsRunbookData())
	require.NoError(t, err)

	ops := results["day-to-day-ops.md"]

	assert.Contains(t, ops, "eks")
	assert.Contains(t, ops, "aws eks update-kubeconfig")
	assert.Contains(t, ops, "Secrets Manager")
	assert.Contains(t, ops, "ACM")
	assert.Contains(t, ops, "ptd ensure acme-prod")
}

func TestRunbook_DayToDayOps_Azure_Content(t *testing.T) {
	results, err := GenerateRunbooks(azureRunbookData())
	require.NoError(t, err)

	ops := results["day-to-day-ops.md"]

	assert.Contains(t, ops, "aks")
	assert.Contains(t, ops, "az aks get-credentials")
	assert.Contains(t, ops, "Key Vault")
	assert.Contains(t, ops, "rsg-ptd-contoso-staging")
	assert.Contains(t, ops, "ptd ensure contoso-staging")
}

func TestRunbook_DayToDayOps_AWS_SitesRendered(t *testing.T) {
	results, err := GenerateRunbooks(awsRunbookData())
	require.NoError(t, err)

	ops := results["day-to-day-ops.md"]

	assert.Contains(t, ops, "connect.acme.com")
	assert.Contains(t, ops, "dev.acme.com")
}

func TestRunbook_DisasterRecovery_AWS_ContainsSections(t *testing.T) {
	results, err := GenerateRunbooks(awsRunbookData())
	require.NoError(t, err)

	dr := results["disaster-recovery.md"]

	assert.Contains(t, dr, "# Disaster Recovery — acme-prod")
	assert.Contains(t, dr, "## Pulumi State Recovery")
	assert.Contains(t, dr, "## Database Recovery")
	assert.Contains(t, dr, "## Storage Recovery")
	assert.Contains(t, dr, "## Kubernetes Cluster Recovery")
	assert.Contains(t, dr, "## DNS and Ingress Recovery")
	assert.Contains(t, dr, "## Full Environment Rebuild")
}

func TestRunbook_DisasterRecovery_AWS_Content(t *testing.T) {
	results, err := GenerateRunbooks(awsRunbookData())
	require.NoError(t, err)

	dr := results["disaster-recovery.md"]

	assert.Contains(t, dr, "ptd-acme-prod")
	assert.Contains(t, dr, "aws rds restore-db-instance-to-point-in-time")
	assert.Contains(t, dr, "aws fsx describe-backups")
	assert.Contains(t, dr, "S3 data buckets have no versioning")
	assert.Contains(t, dr, "ptd ensure acme-prod --only-steps eks")
}

func TestRunbook_DisasterRecovery_Azure_Content(t *testing.T) {
	results, err := GenerateRunbooks(azureRunbookData())
	require.NoError(t, err)

	dr := results["disaster-recovery.md"]

	assert.Contains(t, dr, "Azure Blob Storage")
	assert.Contains(t, dr, "az postgres flexible-server restore")
	assert.Contains(t, dr, "Azure storage has no versioning or soft delete")
	assert.Contains(t, dr, "rsg-ptd-contoso-staging")
	assert.Contains(t, dr, "ptd ensure contoso-staging --only-steps aks")
}

func TestRunbook_DisasterRecovery_AWS_SitesRendered(t *testing.T) {
	results, err := GenerateRunbooks(awsRunbookData())
	require.NoError(t, err)

	dr := results["disaster-recovery.md"]

	assert.Contains(t, dr, "dig connect.acme.com")
	assert.Contains(t, dr, "dig dev.acme.com")
}

func TestRunbooks_NoBannedFlags(t *testing.T) {
	for _, cloud := range []string{"aws", "azure"} {
		t.Run(cloud, func(t *testing.T) {
			data := &RunbookData{
				WorkloadName:  "test-workload",
				Cloud:         cloud,
				Region:        "us-east-1",
				ClusterName:   "test-cluster",
				ResourceGroup: "rsg-ptd-test",
				Sites: []SiteData{
					{Name: "main", Domain: "test.example.com"},
				},
			}
			results, err := GenerateRunbooks(data)
			require.NoError(t, err)

			for filename, content := range results {
				assert.NotContains(t, content, "--auto-apply",
					"%s for %s should not contain --auto-apply", filename, cloud)
				assert.NotContains(t, content, "--dry-run",
					"%s for %s should not contain --dry-run", filename, cloud)
			}
		})
	}
}

func TestRunbooks_NoPulumiCommands(t *testing.T) {
	for _, cloud := range []string{"aws", "azure"} {
		t.Run(cloud, func(t *testing.T) {
			data := &RunbookData{
				WorkloadName:  "test-workload",
				Cloud:         cloud,
				Region:        "us-east-1",
				ClusterName:   "test-cluster",
				ResourceGroup: "rsg-ptd-test",
				Sites: []SiteData{
					{Name: "main", Domain: "test.example.com"},
				},
			}
			results, err := GenerateRunbooks(data)
			require.NoError(t, err)

			for filename, content := range results {
				assert.NotContains(t, content, "pulumi up",
					"%s for %s should not contain 'pulumi up'", filename, cloud)
				assert.NotContains(t, content, "pulumi preview",
					"%s for %s should not contain 'pulumi preview'", filename, cloud)
				assert.NotContains(t, content, "pulumi stack select",
					"%s for %s should not contain 'pulumi stack select'", filename, cloud)
				assert.NotContains(t, content, "pulumi import",
					"%s for %s should not contain 'pulumi import'", filename, cloud)
			}
		})
	}
}

func TestRunbook_DayToDayOps_AWS_StepTable(t *testing.T) {
	results, err := GenerateRunbooks(awsRunbookData())
	require.NoError(t, err)

	ops := results["day-to-day-ops.md"]

	for _, step := range []string{"bootstrap", "persistent", "postgres_config", "eks", "clusters", "helm", "sites"} {
		assert.Contains(t, ops, "| "+step+" |", "step table should contain %s", step)
	}
}

func TestRunbook_DayToDayOps_Azure_StepTable(t *testing.T) {
	results, err := GenerateRunbooks(azureRunbookData())
	require.NoError(t, err)

	ops := results["day-to-day-ops.md"]

	for _, step := range []string{"bootstrap", "persistent", "postgres_config", "aks", "clusters", "helm", "sites"} {
		assert.Contains(t, ops, "| "+step+" |", "step table should contain %s", step)
	}
	assert.NotContains(t, ops, "| eks |", "Azure runbook should not contain eks step")
}

func TestRunbook_RenderDayToDayOps_WritesToWriter(t *testing.T) {
	var buf strings.Builder
	err := RenderDayToDayOps(&buf, awsRunbookData())

	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Day-to-Day Operations")
}

func TestRunbook_RenderDisasterRecovery_WritesToWriter(t *testing.T) {
	var buf strings.Builder
	err := RenderDisasterRecovery(&buf, azureRunbookData())

	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Disaster Recovery")
}

func TestRunbook_DayToDayOps_PtdWorkonCommands(t *testing.T) {
	results, err := GenerateRunbooks(awsRunbookData())
	require.NoError(t, err)

	ops := results["day-to-day-ops.md"]

	assert.Contains(t, ops, "ptd workon acme-prod --")
}

func TestRunbook_DisasterRecovery_FullRebuild(t *testing.T) {
	results, err := GenerateRunbooks(awsRunbookData())
	require.NoError(t, err)

	dr := results["disaster-recovery.md"]

	rebuildStart := strings.Index(dr, "## Full Environment Rebuild")
	require.Greater(t, rebuildStart, 0, "should contain Full Environment Rebuild section")
	rebuild := dr[rebuildStart:]

	assert.Contains(t, rebuild, "ptd ensure acme-prod\n")
	assert.NotContains(t, rebuild, "--only-steps", "full rebuild should run all steps, not individual ones")
}

func TestRunbook_AWS_ClusterNameRendered(t *testing.T) {
	results, err := GenerateRunbooks(awsRunbookData())
	require.NoError(t, err)

	ops := results["day-to-day-ops.md"]
	assert.Contains(t, ops, "aws eks update-kubeconfig --name default_acme-prod-control-plane --region us-east-1")
}

func TestRunbook_Azure_ClusterNameRendered(t *testing.T) {
	results, err := GenerateRunbooks(azureRunbookData())
	require.NoError(t, err)

	ops := results["day-to-day-ops.md"]
	assert.Contains(t, ops, "az aks get-credentials --resource-group rsg-ptd-contoso-staging --name aks-ptd-contoso")
}

func TestBuildRunbookData_AWS_SortsSites(t *testing.T) {
	config := types.AWSWorkloadConfig{
		Region: "us-east-1",
		Sites: map[string]types.SiteConfig{
			"zebra":  {Spec: types.SiteConfigSpec{Domain: "z.example.com"}},
			"alpha":  {Spec: types.SiteConfigSpec{Domain: "a.example.com"}},
			"middle": {Spec: types.SiteConfigSpec{Domain: "m.example.com"}},
		},
		Clusters: map[string]types.AWSWorkloadClusterConfig{
			"20240101": {},
		},
	}

	data, err := buildRunbookData(config, "test-workload")
	require.NoError(t, err)

	require.Len(t, data.Sites, 3)
	assert.Equal(t, "alpha", data.Sites[0].Name)
	assert.Equal(t, "middle", data.Sites[1].Name)
	assert.Equal(t, "zebra", data.Sites[2].Name)
}

func TestBuildRunbookData_Azure_SanitizesResourceGroup(t *testing.T) {
	config := types.AzureWorkloadConfig{
		Region: "eastus",
		Sites: map[string]types.SiteConfig{
			"main": {Spec: types.SiteConfigSpec{Domain: "test.example.com"}},
		},
		Clusters: map[string]types.AzureWorkloadClusterConfig{
			"20240101": {},
		},
	}

	data, err := buildRunbookData(config, "MyWorkload_Test")
	require.NoError(t, err)

	assert.Equal(t, "rsg-ptd-myworkload-test", data.ResourceGroup)
}

func TestBuildRunbookData_AWS_ClusterName(t *testing.T) {
	config := types.AWSWorkloadConfig{
		Region: "us-west-2",
		Clusters: map[string]types.AWSWorkloadClusterConfig{
			"20240601": {},
		},
	}

	data, err := buildRunbookData(config, "acme-prod")
	require.NoError(t, err)

	assert.Equal(t, "default_acme-prod-20240601-control-plane", data.ClusterName)
}

func TestBuildRunbookData_Azure_ClusterName(t *testing.T) {
	config := types.AzureWorkloadConfig{
		Region: "eastus",
		Clusters: map[string]types.AzureWorkloadClusterConfig{
			"20240601": {},
		},
	}

	data, err := buildRunbookData(config, "Contoso-Staging")
	require.NoError(t, err)

	assert.Equal(t, "contoso-staging-20240601", data.ClusterName)
}
