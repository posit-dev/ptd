package steps

import (
	"context"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/posit-dev/ptd/lib/types/typestest"
)

func TestPostgresConfigStepName(t *testing.T) {
	step := &PostgresConfigStep{}
	assert.Equal(t, "postgres_config", step.Name())
}

func TestPostgresConfigStepProxyRequired(t *testing.T) {
	step := &PostgresConfigStep{}
	assert.True(t, step.ProxyRequired())
}

func TestPostgresConfigStepNilTarget(t *testing.T) {
	step := &PostgresConfigStep{}
	step.Set(nil, nil, StepOptions{})
	err := step.Run(context.Background())
	assert.ErrorContains(t, err, "postgres_config step requires a destination target")
}

func TestParseDBSecretField(t *testing.T) {
	t.Run("valid secret", func(t *testing.T) {
		pw, err := parseDBSecretField(`{"password":"supersecret","username":"postgres"}`, "password")
		require.NoError(t, err)
		assert.Equal(t, "supersecret", pw)
	})

	t.Run("missing field", func(t *testing.T) {
		pw, err := parseDBSecretField(`{"username":"postgres"}`, "password")
		require.NoError(t, err)
		assert.Equal(t, "", pw)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := parseDBSecretField(`not-json`, "password")
		assert.Error(t, err)
	})
}

// postgresConfigMocks implements pulumi.MockResourceMonitor for testing the deploy function.
type postgresConfigMocks struct {
	mu        sync.Mutex
	resources []pulumi.MockResourceArgs
}

func (m *postgresConfigMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources = append(m.resources, args)
	return args.Name + "_id", args.Inputs, nil
}

func (m *postgresConfigMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func mockAWSTarget(name string, isControlRoom bool) *typestest.MockTarget {
	tgt := &typestest.MockTarget{}
	tgt.On("Name").Return(name)
	tgt.On("CloudProvider").Return(types.AWS)
	tgt.On("ControlRoom").Return(isControlRoom)

	if isControlRoom {
		tgt.On("Type").Return(types.TargetTypeControlRoom)
	} else {
		tgt.On("Type").Return(types.TargetTypeWorkload)
	}

	return tgt
}

func mockAzureTarget(name string) *typestest.MockTarget {
	tgt := &typestest.MockTarget{}
	tgt.On("Name").Return(name)
	tgt.On("CloudProvider").Return(types.Azure)
	tgt.On("ControlRoom").Return(false)
	tgt.On("Type").Return(types.TargetTypeWorkload)
	return tgt
}

// --- AWS deploy tests ---

func TestAWSPostgresConfigDeployControlRoom(t *testing.T) {
	mocks := &postgresConfigMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		target := mockAWSTarget("test-aws-ctrl", true)
		return awsPostgresConfigDeploy(ctx, target, "db.example.com", "master-pw", nil)
	}, pulumi.WithMocks("ptd-aws-control-room-postgres-config", "test-aws-ctrl", mocks))

	require.NoError(t, err)

	// Control room: 5 resources (password, provider, role, db, grant)
	assert.Len(t, mocks.resources, 5, "control room should create exactly 5 resources")

	resourceNames := make([]string, len(mocks.resources))
	for i, r := range mocks.resources {
		resourceNames[i] = r.Name
	}

	assert.Contains(t, resourceNames, "test-aws-ctrl-db-grafana-pw")
	assert.Contains(t, resourceNames, "test-aws-ctrl-postgres-provider")
	assert.Contains(t, resourceNames, "test-aws-ctrl-grafana-role")
	assert.Contains(t, resourceNames, "test-aws-ctrl-grafana-db")
	assert.Contains(t, resourceNames, "test-aws-ctrl-grafana-grant")

	// Verify password length
	for _, r := range mocks.resources {
		if r.Name == "test-aws-ctrl-db-grafana-pw" {
			assert.Equal(t, resource.NewNumberProperty(36), r.Inputs["length"])
		}
	}

	// Verify role/db names use plain "grafana" for control room
	for _, r := range mocks.resources {
		if r.Name == "test-aws-ctrl-grafana-role" {
			assert.Equal(t, resource.NewStringProperty("grafana"), r.Inputs["name"])
		}
		if r.Name == "test-aws-ctrl-grafana-db" {
			assert.Equal(t, resource.NewStringProperty("grafana"), r.Inputs["name"])
		}
	}
}

func TestAWSPostgresConfigDeployWorkload(t *testing.T) {
	mocks := &postgresConfigMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		target := mockAWSTarget("test-aws-staging", false)
		return awsPostgresConfigDeploy(ctx, target, "db.example.com", "master-pw", nil)
	}, pulumi.WithMocks("ptd-aws-workload-postgres-config", "test-aws-staging", mocks))

	require.NoError(t, err)

	assert.Len(t, mocks.resources, 5, "workload should create exactly 5 resources")

	for _, r := range mocks.resources {
		if r.Name == "test-aws-staging-grafana-role" {
			assert.Equal(t, resource.NewStringProperty("grafana-test-aws-staging"), r.Inputs["name"])
		}
		if r.Name == "test-aws-staging-grafana-db" {
			assert.Equal(t, resource.NewStringProperty("grafana-test-aws-staging"), r.Inputs["name"])
		}
	}
}

func TestAWSPostgresConfigDeployWorkloadWithExtraDbs(t *testing.T) {
	mocks := &postgresConfigMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		target := mockAWSTarget("test-aws-staging", false)
		return awsPostgresConfigDeploy(ctx, target, "db.example.com", "master-pw", []string{"soleng"})
	}, pulumi.WithMocks("ptd-aws-workload-postgres-config", "test-aws-staging", mocks))

	require.NoError(t, err)

	// 5 base + 4 extra (pw, role, db, grant) = 9
	assert.Len(t, mocks.resources, 9, "workload with extra_postgres_dbs=[soleng] should create 9 resources")

	resourceNames := make([]string, len(mocks.resources))
	for i, r := range mocks.resources {
		resourceNames[i] = r.Name
	}

	assert.Contains(t, resourceNames, "test-aws-staging-db-soleng-pw")
	assert.Contains(t, resourceNames, "test-aws-staging-soleng-role")
	assert.Contains(t, resourceNames, "test-aws-staging-soleng-db")
	assert.Contains(t, resourceNames, "test-aws-staging-soleng-grant")
}

func TestAWSPostgresConfigDeployExtraDbHyphenSanitization(t *testing.T) {
	mocks := &postgresConfigMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		target := mockAWSTarget("test-aws-staging", false)
		return awsPostgresConfigDeploy(ctx, target, "db.example.com", "master-pw", []string{"sol-eng"})
	}, pulumi.WithMocks("ptd-aws-workload-postgres-config", "test-aws-staging", mocks))

	require.NoError(t, err)

	for _, r := range mocks.resources {
		if r.Name == "test-aws-staging-sol_eng-role" {
			assert.Equal(t, resource.NewStringProperty("sol_eng"), r.Inputs["name"])
		}
	}
}

func TestAWSPostgresConfigDeployProviderConfig(t *testing.T) {
	mocks := &postgresConfigMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		target := mockAWSTarget("test-aws-ctrl", true)
		return awsPostgresConfigDeploy(ctx, target, "mydb.rds.amazonaws.com", "s3cret", nil)
	}, pulumi.WithMocks("ptd-aws-control-room-postgres-config", "test-aws-ctrl", mocks))

	require.NoError(t, err)

	for _, r := range mocks.resources {
		if r.Name == "test-aws-ctrl-postgres-provider" {
			assert.Equal(t, resource.NewStringProperty("mydb.rds.amazonaws.com"), r.Inputs["host"])
			assert.Equal(t, resource.NewNumberProperty(5432), r.Inputs["port"])
			assert.Equal(t, resource.NewStringProperty("require"), r.Inputs["sslmode"])
			assert.Equal(t, resource.NewStringProperty("postgres"), r.Inputs["username"])
			assert.Equal(t, resource.MakeSecret(resource.NewStringProperty("s3cret")), r.Inputs["password"])
		}
	}
}

// --- Azure deploy tests ---

func TestAzurePostgresConfigDeploySingleCluster(t *testing.T) {
	mocks := &postgresConfigMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		target := mockAzureTarget("test-az-staging")
		params := azurePostgresParams{
			dbHost:                     "db.postgres.azure.com",
			dbUser:                     "adminuser",
			dbPassword:                 "admin-pw",
			clusters:                   []string{"20240101"},
			vaultName:                  "kv-ptd-test-az-staging",
			resourceGroupName:          "rsg-ptd-test-az-staging",
			protectPersistentResources: false,
		}
		return azurePostgresConfigDeploy(ctx, target, params)
	}, pulumi.WithMocks("ptd-azure-workload-postgres-config", "test-az-staging", mocks))

	require.NoError(t, err)

	// 1 provider + 1 cluster * (pw + role + db + grant + kv secret) = 6 resources
	assert.Len(t, mocks.resources, 6, "single cluster should create 6 resources")

	resourceNames := make([]string, len(mocks.resources))
	for i, r := range mocks.resources {
		resourceNames[i] = r.Name
	}

	assert.Contains(t, resourceNames, "test-az-staging-grafana-postgres-provider")
	assert.Contains(t, resourceNames, "test-az-staging-20240101-db-grafana-pw")
	assert.Contains(t, resourceNames, "test-az-staging-20240101-grafana-role")
	assert.Contains(t, resourceNames, "test-az-staging-20240101-grafana-db")
	assert.Contains(t, resourceNames, "test-az-staging-20240101-grafana-grant")
	assert.Contains(t, resourceNames, "test-az-staging-20240101-postgres-grafana-user")

	// Verify role naming: grafana-{name}-{release}
	for _, r := range mocks.resources {
		if r.Name == "test-az-staging-20240101-grafana-role" {
			assert.Equal(t, resource.NewStringProperty("grafana-test-az-staging-20240101"), r.Inputs["name"])
		}
	}
}

func TestAzurePostgresConfigDeployMultipleClusters(t *testing.T) {
	mocks := &postgresConfigMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		target := mockAzureTarget("test-az-staging")
		params := azurePostgresParams{
			dbHost:            "db.postgres.azure.com",
			dbUser:            "adminuser",
			dbPassword:        "admin-pw",
			clusters:          []string{"20240101", "20240201"},
			vaultName:         "kv-ptd-test-az-staging",
			resourceGroupName: "rsg-ptd-test-az-staging",
		}
		return azurePostgresConfigDeploy(ctx, target, params)
	}, pulumi.WithMocks("ptd-azure-workload-postgres-config", "test-az-staging", mocks))

	require.NoError(t, err)

	// 1 provider + 2 clusters * 5 resources = 11
	assert.Len(t, mocks.resources, 11, "two clusters should create 11 resources")
}

func TestAzurePostgresConfigDeployProviderConfig(t *testing.T) {
	mocks := &postgresConfigMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		target := mockAzureTarget("test-az-staging")
		params := azurePostgresParams{
			dbHost:            "mydb.postgres.database.azure.com",
			dbUser:            "pgadmin",
			dbPassword:        "az-secret",
			clusters:          []string{"20240101"},
			vaultName:         "kv-ptd-test-az-staging",
			resourceGroupName: "rsg-ptd-test-az-staging",
		}
		return azurePostgresConfigDeploy(ctx, target, params)
	}, pulumi.WithMocks("ptd-azure-workload-postgres-config", "test-az-staging", mocks))

	require.NoError(t, err)

	for _, r := range mocks.resources {
		if r.Name == "test-az-staging-grafana-postgres-provider" {
			assert.Equal(t, resource.NewStringProperty("mydb.postgres.database.azure.com"), r.Inputs["host"])
			assert.Equal(t, resource.NewStringProperty("pgadmin"), r.Inputs["username"])
			assert.Equal(t, resource.MakeSecret(resource.NewStringProperty("az-secret")), r.Inputs["password"])
		}
	}
}

func TestAzurePostgresConfigDeployKeyVaultSecret(t *testing.T) {
	mocks := &postgresConfigMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		target := mockAzureTarget("test-az-staging")
		params := azurePostgresParams{
			dbHost:            "db.postgres.azure.com",
			dbUser:            "adminuser",
			dbPassword:        "admin-pw",
			clusters:          []string{"20240101"},
			vaultName:         "kv-ptd-test-az-staging",
			resourceGroupName: "rsg-ptd-test-az-staging",
		}
		return azurePostgresConfigDeploy(ctx, target, params)
	}, pulumi.WithMocks("ptd-azure-workload-postgres-config", "test-az-staging", mocks))

	require.NoError(t, err)

	// Verify the Key Vault secret resource
	for _, r := range mocks.resources {
		if r.Name == "test-az-staging-20240101-postgres-grafana-user" {
			assert.Equal(t, resource.NewStringProperty("test-az-staging-20240101-postgres-grafana-user"), r.Inputs["secretName"])
			assert.Equal(t, resource.NewStringProperty("kv-ptd-test-az-staging"), r.Inputs["vaultName"])
			assert.Equal(t, resource.NewStringProperty("rsg-ptd-test-az-staging"), r.Inputs["resourceGroupName"])
		}
	}
}
