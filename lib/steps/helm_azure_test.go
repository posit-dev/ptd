package steps

import (
	"testing"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
	yamlv2 "gopkg.in/yaml.v2"
)

// runAzureHelmAlloy invokes azureHelmAlloy in isolation with no-op providers/aliases.
// It reuses the helmAWSMocks recorder from helm_aws_test.go (same package).
func runAzureHelmAlloy(t *testing.T, controlRoomDomain string) *helmAWSMocks {
	t.Helper()
	mocks := &helmAWSMocks{}
	noopOpt := pulumi.Aliases(nil)
	withAlias := func(string, string) pulumi.ResourceOption { return pulumi.Aliases(nil) }
	withNestedAlias := func(string, string, string) pulumi.ResourceOption { return pulumi.Aliases(nil) }
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := azureHelmParams{
			compoundName:      "wl01-staging",
			trueName:          "wl01",
			environment:       "staging",
			subscriptionID:    "00000000-0000-0000-0000-000000000000",
			tenantID:          "11111111-1111-1111-1111-111111111111",
			region:            "eastus",
			resourceGroupName: "rsg-ptd-wl01-staging",
			mimirPassword:     "s3cr3t",
			cfg: types.AzureWorkloadConfig{
				ControlRoomDomain: controlRoomDomain,
			},
		}
		return azureHelmAlloy(ctx, noopOpt, "wl01-staging", "20250101", params,
			"", "0.12.6", withAlias, withNestedAlias)
	}, pulumi.WithMocks("ptd-azure-workload-helm", "wl01-staging", mocks))
	require.NoError(t, err)
	return mocks
}

// azureAlloyChartValues unmarshals the Alloy HelmChart CR's valuesContent for inspection.
func azureAlloyChartValues(t *testing.T, mocks *helmAWSMocks) map[string]interface{} {
	t.Helper()
	chart := mocks.find("wl01-staging-20250101-grafana-alloy-release")
	require.NotNil(t, chart, "alloy HelmChart CR not created")
	valuesContent := chart.Inputs["spec"].ObjectValue()["valuesContent"].StringValue()
	var values map[string]interface{}
	require.NoError(t, yamlv2.Unmarshal([]byte(valuesContent), &values))
	return values
}

func TestAzureHelmAlloyWithControlRoom(t *testing.T) {
	mocks := runAzureHelmAlloy(t, "ctrl.example.posit.team")

	// The mimir-auth Secret is created when the workload has a control room.
	secret := mocks.find("wl01-staging-20250101-mimir-auth")
	require.NotNil(t, secret, "mimir-auth Secret should be created when a control room is configured")

	values := azureAlloyChartValues(t, mocks)

	// The controller always carries the workload-identity podLabels, plus the mimir-auth volume.
	controller := values["controller"].(map[interface{}]interface{})
	require.Contains(t, controller, "podLabels")
	volumes, ok := controller["volumes"].(map[interface{}]interface{})
	require.True(t, ok, "controller.volumes should be present when a control room is configured")
	require.Contains(t, volumes, "extra")

	// The alloy mounts include the mimir-auth mount alongside varlog.
	mounts := values["alloy"].(map[interface{}]interface{})["mounts"].(map[interface{}]interface{})
	require.Contains(t, mounts, "extra", "mimir-auth mount should be present when a control room is configured")
	require.Contains(t, mounts, "varlog")
}

func TestAzureHelmAlloyWithoutControlRoom(t *testing.T) {
	mocks := runAzureHelmAlloy(t, "")

	// The mimir-auth Secret is NOT created when there is no control room.
	secret := mocks.find("wl01-staging-20250101-mimir-auth")
	require.Nil(t, secret, "mimir-auth Secret should be omitted when there is no control room")

	values := azureAlloyChartValues(t, mocks)

	// The controller key is still present (it carries the workload-identity podLabels),
	// but the mimir-auth volume is omitted.
	controller := values["controller"].(map[interface{}]interface{})
	require.Contains(t, controller, "podLabels")
	require.NotContains(t, controller, "volumes", "controller.volumes should be omitted when there is no control room")

	// varlog is still present but the mimir-auth mount is gone.
	mounts := values["alloy"].(map[interface{}]interface{})["mounts"].(map[interface{}]interface{})
	require.NotContains(t, mounts, "extra", "mimir-auth mount should be omitted when there is no control room")
	require.Contains(t, mounts, "varlog")
}
