package steps

import (
	"sync"
	"testing"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv2 "gopkg.in/yaml.v2"
)

// helmAWSMocks records created resources for inspection.
type helmAWSMocks struct {
	mu        sync.Mutex
	resources []pulumi.MockResourceArgs
}

func (m *helmAWSMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources = append(m.resources, args)
	return args.Name + "_id", args.Inputs, nil
}

func (m *helmAWSMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *helmAWSMocks) find(name string) *pulumi.MockResourceArgs {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.resources {
		if m.resources[i].Name == name {
			return &m.resources[i]
		}
	}
	return nil
}

// runAwsHelmTraefik invokes awsHelmTraefik in isolation with no-op providers/aliases.
func runAwsHelmTraefik(t *testing.T, replicas int) *helmAWSMocks {
	t.Helper()
	mocks := &helmAWSMocks{}
	noopOpt := pulumi.Aliases(nil)
	withAlias := func(string, string) pulumi.ResourceOption { return pulumi.Aliases(nil) }
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := awsHelmParams{
			compoundName: "wl01-staging",
			trueName:     "wl01",
			environment:  "staging",
			cfg:          types.AWSWorkloadConfig{},
		}
		return awsHelmTraefik(ctx, noopOpt, "wl01-staging", "20250101", params, "100", "37.1.2", replicas, withAlias)
	}, pulumi.WithMocks("ptd-aws-workload-helm", "wl01-staging", mocks))
	require.NoError(t, err)
	return mocks
}

func TestAwsHelmTraefikHA(t *testing.T) {
	mocks := runAwsHelmTraefik(t, 3)

	// A dedicated traefik-critical PriorityClass is created.
	pc := mocks.find("wl01-staging-20250101-traefik-critical-priority")
	require.NotNil(t, pc, "traefik-critical PriorityClass not created")
	assert.Equal(t, "traefik-critical", pc.Inputs["metadata"].ObjectValue()["name"].StringValue())
	assert.Equal(t, 1000000000.0, pc.Inputs["value"].NumberValue())

	// The HelmChart CR carries the HA values in its valuesContent YAML.
	chart := mocks.find("wl01-staging-20250101-traefik-helm-release")
	require.NotNil(t, chart, "traefik HelmChart CR not created")
	valuesContent := chart.Inputs["spec"].ObjectValue()["valuesContent"].StringValue()

	var values map[string]interface{}
	require.NoError(t, yamlv2.Unmarshal([]byte(valuesContent), &values))

	deployment := values["deployment"].(map[interface{}]interface{})
	assert.Equal(t, 3, deployment["replicas"])
	assert.Equal(t, "Deployment", deployment["kind"])

	res := values["resources"].(map[interface{}]interface{})
	requests := res["requests"].(map[interface{}]interface{})
	assert.Equal(t, "200m", requests["cpu"])
	assert.Equal(t, "256Mi", requests["memory"])
	assert.Contains(t, res, "limits")

	tsc := values["topologySpreadConstraints"].([]interface{})
	require.Len(t, tsc, 1)
	assert.Equal(t, "kubernetes.io/hostname", tsc[0].(map[interface{}]interface{})["topologyKey"])

	pdb := values["podDisruptionBudget"].(map[interface{}]interface{})
	assert.Equal(t, true, pdb["enabled"])
	assert.Equal(t, 1, pdb["maxUnavailable"])

	assert.Equal(t, "traefik-critical", values["priorityClassName"])
}

// runAwsHelmAlloy invokes awsHelmAlloy in isolation with no-op providers/aliases.
func runAwsHelmAlloy(t *testing.T, controlRoomDomain string) *helmAWSMocks {
	t.Helper()
	mocks := &helmAWSMocks{}
	noopOpt := pulumi.Aliases(nil)
	withAlias := func(string, string) pulumi.ResourceOption { return pulumi.Aliases(nil) }
	withNestedAlias := func(string, string, string) pulumi.ResourceOption { return pulumi.Aliases(nil) }
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		params := awsHelmParams{
			compoundName:  "wl01-staging",
			trueName:      "wl01",
			environment:   "staging",
			accountID:     "123456789012",
			region:        "us-east-1",
			mimirPassword: "s3cr3t",
			cfg: types.AWSWorkloadConfig{
				ControlRoomDomain: controlRoomDomain,
			},
		}
		resolved := types.ResolvedAWSComponents{AlloyVersion: "0.12.6"}
		return awsHelmAlloy(ctx, noopOpt, "wl01-staging", "20250101", params, resolved,
			types.AWSWorkloadClusterSpec{}, withAlias, withNestedAlias)
	}, pulumi.WithMocks("ptd-aws-workload-helm", "wl01-staging", mocks))
	require.NoError(t, err)
	return mocks
}

// alloyChartValues unmarshals the Alloy HelmChart CR's valuesContent for inspection.
func alloyChartValues(t *testing.T, mocks *helmAWSMocks) map[string]interface{} {
	t.Helper()
	chart := mocks.find("wl01-staging-20250101-grafana-alloy-release")
	require.NotNil(t, chart, "alloy HelmChart CR not created")
	valuesContent := chart.Inputs["spec"].ObjectValue()["valuesContent"].StringValue()
	var values map[string]interface{}
	require.NoError(t, yamlv2.Unmarshal([]byte(valuesContent), &values))
	return values
}

func TestAwsHelmAlloyWithControlRoom(t *testing.T) {
	mocks := runAwsHelmAlloy(t, "ctrl.example.posit.team")

	// The mimir-auth Secret is created when the workload has a control room.
	secret := mocks.find("wl01-staging-20250101-alloy-mimir-auth")
	require.NotNil(t, secret, "mimir-auth Secret should be created when a control room is configured")

	values := alloyChartValues(t, mocks)

	// The controller carries the mimir-auth volume.
	controller, ok := values["controller"].(map[interface{}]interface{})
	require.True(t, ok, "controller key should be present when a control room is configured")
	volumes := controller["volumes"].(map[interface{}]interface{})
	require.Contains(t, volumes, "extra")

	// The alloy mounts include the mimir-auth mount.
	mounts := values["alloy"].(map[interface{}]interface{})["mounts"].(map[interface{}]interface{})
	require.Contains(t, mounts, "extra", "mimir-auth mount should be present when a control room is configured")
	require.Contains(t, mounts, "varlog")
}

func TestAwsHelmAlloyWithoutControlRoom(t *testing.T) {
	mocks := runAwsHelmAlloy(t, "")

	// The mimir-auth Secret is NOT created when there is no control room.
	secret := mocks.find("wl01-staging-20250101-alloy-mimir-auth")
	require.Nil(t, secret, "mimir-auth Secret should be omitted when there is no control room")

	values := alloyChartValues(t, mocks)

	// The controller key (which only carries the mimir-auth volume on AWS) is omitted entirely.
	_, hasController := values["controller"]
	require.False(t, hasController, "controller key should be omitted when there is no control room")

	// varlog is still present but the mimir-auth mount is gone.
	mounts := values["alloy"].(map[interface{}]interface{})["mounts"].(map[interface{}]interface{})
	require.NotContains(t, mounts, "extra", "mimir-auth mount should be omitted when there is no control room")
	require.Contains(t, mounts, "varlog")
}

func TestAwsHelmTraefikReplicasOverride(t *testing.T) {
	mocks := runAwsHelmTraefik(t, 5)

	chart := mocks.find("wl01-staging-20250101-traefik-helm-release")
	require.NotNil(t, chart)
	valuesContent := chart.Inputs["spec"].ObjectValue()["valuesContent"].StringValue()

	var values map[string]interface{}
	require.NoError(t, yamlv2.Unmarshal([]byte(valuesContent), &values))

	deployment := values["deployment"].(map[interface{}]interface{})
	assert.Equal(t, 5, deployment["replicas"])
}
