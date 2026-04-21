package steps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-azure-native-sdk/keyvault/v3"
	"github.com/pulumi/pulumi-postgresql/sdk/v3/go/postgresql"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/proxy"
	"github.com/posit-dev/ptd/lib/types"
)

type PostgresConfigStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *PostgresConfigStep) Name() string {
	return "postgres_config"
}

func (s *PostgresConfigStep) ProxyRequired() bool {
	return true // connects to database
}

func (s *PostgresConfigStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *PostgresConfigStep) Run(ctx context.Context) error {
	if s.DstTarget == nil {
		return errors.New("postgres_config step requires a destination target")
	}

	creds, err := s.DstTarget.Credentials(ctx)
	if err != nil {
		return err
	}
	envVars, err := prepareEnvVarsForPulumi(ctx, s.DstTarget, creds)
	if err != nil {
		return err
	}

	// if the target isn't tailscale enabled, add ALL_PROXY to the env vars
	if !s.DstTarget.TailscaleEnabled() {
		envVars["ALL_PROXY"] = fmt.Sprintf("socks5://localhost:%d", proxy.WorkloadPort(s.DstTarget.Name()))
	}

	switch s.DstTarget.CloudProvider() {
	case types.AWS:
		return s.runAWSInlineGo(ctx, creds, envVars)
	case types.Azure:
		return s.runAzureInlineGo(ctx, creds, envVars)
	default:
		return fmt.Errorf("unsupported cloud provider for postgres_config: %s", s.DstTarget.CloudProvider())
	}
}

// --- AWS ---

func (s *PostgresConfigStep) runAWSInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	// Fetch persistent stack outputs (db_address, db_secret_arn)
	persistentOutputs, err := getPersistentStackOutputs(ctx, s.DstTarget)
	if err != nil {
		return fmt.Errorf("failed to get persistent stack outputs: %w", err)
	}

	dbAddressOutput, ok := persistentOutputs["db_address"]
	if !ok {
		return fmt.Errorf("db_address output not found in persistent stack outputs")
	}
	dbAddress := dbAddressOutput.Value.(string)

	dbSecretArnOutput, ok := persistentOutputs["db_secret_arn"]
	if !ok {
		return fmt.Errorf("db_secret_arn output not found in persistent stack outputs")
	}
	dbSecretArn := dbSecretArnOutput.Value.(string)

	// Fetch RDS master password from AWS Secrets Manager
	secretValue, err := s.DstTarget.SecretStore().GetSecretValue(ctx, creds, dbSecretArn)
	if err != nil {
		return fmt.Errorf("failed to get RDS master password: %w", err)
	}
	masterPassword, err := parseDBSecretField(secretValue, "password")
	if err != nil {
		return err
	}

	// Determine extra postgres DBs (workload only)
	var extraPostgresDbs []string
	if !s.DstTarget.ControlRoom() {
		c, err := helpers.ConfigForTarget(s.DstTarget)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if config, ok := c.(types.AWSWorkloadConfig); ok {
			extraPostgresDbs = config.ExtraPostgresDbs
		}
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, target types.Target) error {
		return awsPostgresConfigDeploy(pctx, target, dbAddress, masterPassword, extraPostgresDbs)
	}, envVars)
	if err != nil {
		return err
	}

	return runPulumi(ctx, stack, s.Options)
}

// --- Azure ---

// azurePostgresParams holds the pre-fetched data needed by the Azure deploy function.
type azurePostgresParams struct {
	dbHost                     string
	dbUser                     string
	dbPassword                 string
	clusters                   []string // release names
	vaultName                  string
	resourceGroupName          string
	protectPersistentResources bool
}

func (s *PostgresConfigStep) runAzureInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	azTarget, ok := s.DstTarget.(azure.Target)
	if !ok {
		return errors.New("expected Azure target for postgres_config step")
	}

	vaultName := azTarget.VaultName()
	resourceGroupName := azTarget.ResourceGroupName()

	// Fetch DB admin secret from Azure Key Vault
	secretName := fmt.Sprintf("%s-grafana-postgres-admin-secret", s.DstTarget.Name())
	secretValue, err := s.DstTarget.SecretStore().GetSecretValue(ctx, creds, secretName)
	if err != nil {
		return fmt.Errorf("failed to get postgres admin secret from Key Vault: %w", err)
	}

	var secretData map[string]string
	if err := json.Unmarshal([]byte(secretValue), &secretData); err != nil {
		return fmt.Errorf("failed to parse postgres admin secret JSON: %w", err)
	}

	dbHost := secretData["fqdn"]
	dbUser := secretData["username"]
	dbPassword := secretData["password"]
	if dbHost == "" || dbUser == "" || dbPassword == "" {
		return fmt.Errorf("grafana DB secret must contain 'fqdn', 'username' and 'password' fields")
	}

	// Load config to get cluster releases and protect flag
	c, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	config, ok := c.(types.AzureWorkloadConfig)
	if !ok {
		return errors.New("expected AzureWorkloadConfig for Azure postgres_config step")
	}

	var releases []string
	for release := range config.Clusters {
		releases = append(releases, release)
	}

	params := azurePostgresParams{
		dbHost:                     dbHost,
		dbUser:                     dbUser,
		dbPassword:                 dbPassword,
		clusters:                   releases,
		vaultName:                  vaultName,
		resourceGroupName:          resourceGroupName,
		protectPersistentResources: config.ProtectPersistentResources,
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, target types.Target) error {
		return azurePostgresConfigDeploy(pctx, target, params)
	}, envVars)
	if err != nil {
		return err
	}

	return runPulumi(ctx, stack, s.Options)
}

// --- Shared helpers ---

// parseDBSecretField extracts a field from a JSON secret string.
func parseDBSecretField(secretString string, field string) (string, error) {
	var secretData map[string]string
	if err := json.Unmarshal([]byte(secretString), &secretData); err != nil {
		return "", fmt.Errorf("failed to parse DB secret JSON: %w", err)
	}
	return secretData[field], nil
}

// --- AWS deploy ---

// awsPostgresConfigDeploy creates PostgreSQL databases and roles for Grafana via inline Go Pulumi.
// It handles both control room and workload variants for AWS.
func awsPostgresConfigDeploy(ctx *pulumi.Context, target types.Target, dbAddress string, masterPassword string, extraPostgresDbs []string) error {
	name := target.Name()
	isControlRoom := target.ControlRoom()

	// Python component type for alias resolution
	componentType := "ptd:AWSWorkloadPostgresConfig"
	if isControlRoom {
		componentType = "ptd:AWSControlRoomPostgresConfig"
	}

	// Helper to create alias pointing to old Python component parent URN.
	// Python resources were children of a ComponentResource, so their URNs included
	// the component type as a parent prefix. Go inline resources are children of the
	// Stack, so we need aliases for Pulumi to recognize them as the same resources.
	withAlias := func() pulumi.ResourceOption {
		parentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s::%s", ctx.Stack(), ctx.Project(), componentType, name)
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(parentURN)}})
	}

	// Grafana role/db naming differs between control room and workload
	grafanaRoleName := "grafana"
	grafanaDBName := "grafana"
	if !isControlRoom {
		grafanaRoleName = fmt.Sprintf("grafana-%s", name)
		grafanaDBName = fmt.Sprintf("grafana-%s", name)
	}

	// 1. Random password for Grafana DB user
	grafanaPw, err := random.NewRandomPassword(ctx, fmt.Sprintf("%s-db-grafana-pw", name), &random.RandomPasswordArgs{
		Special:         pulumi.Bool(true),
		OverrideSpecial: pulumi.String("-_"),
		Length:          pulumi.Int(36),
	}, withAlias())
	if err != nil {
		return fmt.Errorf("failed to create grafana password: %w", err)
	}

	// 2. PostgreSQL provider pointing to RDS
	pgProvider, err := postgresql.NewProvider(ctx, fmt.Sprintf("%s-postgres-provider", name), &postgresql.ProviderArgs{
		Host:      pulumi.String(dbAddress),
		Port:      pulumi.Int(5432),
		Sslmode:   pulumi.String("require"),
		Username:  pulumi.String("postgres"),
		Password:  pulumi.String(masterPassword),
		Superuser: pulumi.Bool(false),
	}, withAlias())
	if err != nil {
		return fmt.Errorf("failed to create postgres provider: %w", err)
	}

	// 3. Grafana role
	grafanaRole, err := postgresql.NewRole(ctx, fmt.Sprintf("%s-grafana-role", name), &postgresql.RoleArgs{
		Login:    pulumi.Bool(true),
		Name:     pulumi.String(grafanaRoleName),
		Password: grafanaPw.Result,
	}, pulumi.Provider(pgProvider), withAlias())
	if err != nil {
		return fmt.Errorf("failed to create grafana role: %w", err)
	}

	// 4. Grafana database
	grafanaDbOpts := []pulumi.ResourceOption{
		pulumi.Provider(pgProvider),
		pulumi.DependsOn([]pulumi.Resource{grafanaRole}),
		withAlias(),
	}
	if !isControlRoom {
		grafanaDbOpts = append(grafanaDbOpts, pulumi.Protect(true))
	}
	grafanaDb, err := postgresql.NewDatabase(ctx, fmt.Sprintf("%s-grafana-db", name), &postgresql.DatabaseArgs{
		Name:  pulumi.String(grafanaDBName),
		Owner: pulumi.String(grafanaRoleName),
	}, grafanaDbOpts...)
	if err != nil {
		return fmt.Errorf("failed to create grafana database: %w", err)
	}

	// 5. Grafana grant (CREATE, USAGE on public schema)
	grant, err := postgresql.NewGrant(ctx, fmt.Sprintf("%s-grafana-grant", name), &postgresql.GrantArgs{
		Database:   pulumi.String(grafanaDBName),
		Role:       pulumi.String(grafanaRoleName),
		Schema:     pulumi.String("public"),
		ObjectType: pulumi.String("schema"),
		Privileges: pulumi.StringArray{pulumi.String("CREATE"), pulumi.String("USAGE")},
	}, pulumi.Provider(pgProvider), pulumi.DependsOn([]pulumi.Resource{grafanaDb, grafanaRole}), withAlias())
	if err != nil {
		return fmt.Errorf("failed to create grafana grant: %w", err)
	}

	// Exports
	if isControlRoom {
		ctx.Export("db_grafana_connection", pulumi.Sprintf("postgres://grafana:%s@%s/grafana", grafanaPw.Result, pulumi.String(dbAddress)))
	}
	ctx.Export("db_grafana_pw", grafanaPw.Result)
	ctx.Export("grafana_db_name", grafanaDb.Name)
	ctx.Export("grant_id", grant.ID())

	// Extra databases (workload only, e.g. soleng for staging workloads)
	for _, dbn := range extraPostgresDbs {
		dbName := strings.ReplaceAll(dbn, "-", "_")

		extraPw, err := random.NewRandomPassword(ctx, fmt.Sprintf("%s-db-%s-pw", name, dbName), &random.RandomPasswordArgs{
			Special:         pulumi.Bool(true),
			OverrideSpecial: pulumi.String("-_"),
			Length:          pulumi.Int(36),
		}, withAlias())
		if err != nil {
			return fmt.Errorf("failed to create %s password: %w", dbName, err)
		}

		extraRole, err := postgresql.NewRole(ctx, fmt.Sprintf("%s-%s-role", name, dbName), &postgresql.RoleArgs{
			Login:    pulumi.Bool(true),
			Name:     pulumi.String(dbName),
			Password: extraPw.Result,
		}, pulumi.Provider(pgProvider), withAlias())
		if err != nil {
			return fmt.Errorf("failed to create %s role: %w", dbName, err)
		}

		extraDb, err := postgresql.NewDatabase(ctx, fmt.Sprintf("%s-%s-db", name, dbName), &postgresql.DatabaseArgs{
			Name:  pulumi.String(dbName),
			Owner: pulumi.String(dbName),
		}, pulumi.Provider(pgProvider), pulumi.DependsOn([]pulumi.Resource{extraRole}), pulumi.Protect(true), withAlias())
		if err != nil {
			return fmt.Errorf("failed to create %s database: %w", dbName, err)
		}

		_, err = postgresql.NewGrant(ctx, fmt.Sprintf("%s-%s-grant", name, dbName), &postgresql.GrantArgs{
			Database:   pulumi.String(dbName),
			Role:       pulumi.String(dbName),
			Schema:     pulumi.String("public"),
			ObjectType: pulumi.String("schema"),
			Privileges: pulumi.StringArray{pulumi.String("CREATE"), pulumi.String("USAGE")},
		}, pulumi.Provider(pgProvider), pulumi.DependsOn([]pulumi.Resource{extraDb, extraRole}), withAlias())
		if err != nil {
			return fmt.Errorf("failed to create %s grant: %w", dbName, err)
		}

		ctx.Export(fmt.Sprintf("%s_pw", dbName), extraPw.Result)
	}

	return nil
}

// --- Azure deploy ---

// azurePostgresConfigDeploy creates per-cluster Grafana PostgreSQL resources for Azure workloads.
// For each cluster release, it creates a password, role, database, grant, and a Key Vault secret
// containing the credentials.
func azurePostgresConfigDeploy(ctx *pulumi.Context, target types.Target, params azurePostgresParams) error {
	name := target.Name()

	// Alias helpers for the two levels of Python component nesting:
	// AzureWorkloadPostgresConfig -> provider
	// AzureWorkloadPostgresConfig -> GrafanaPostgresResources -> per-cluster resources
	outerComponentURN := fmt.Sprintf("urn:pulumi:%s::%s::ptd:AzureWorkloadPostgresConfig::%s",
		ctx.Stack(), ctx.Project(), name)

	withProviderAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(outerComponentURN)}})
	}

	innerComponentURN := fmt.Sprintf("urn:pulumi:%s::%s::ptd:AzureWorkloadPostgresConfig$ptd:GrafanaPostgresResources::%s",
		ctx.Stack(), ctx.Project(), name)

	withClusterAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(innerComponentURN)}})
	}

	// PostgreSQL provider pointing to Azure Database for PostgreSQL
	pgProvider, err := postgresql.NewProvider(ctx, fmt.Sprintf("%s-grafana-postgres-provider", name), &postgresql.ProviderArgs{
		Host:      pulumi.String(params.dbHost),
		Port:      pulumi.Int(5432),
		Sslmode:   pulumi.String("require"),
		Username:  pulumi.String(params.dbUser),
		Password:  pulumi.String(params.dbPassword),
		Superuser: pulumi.Bool(false),
	}, withProviderAlias())
	if err != nil {
		return fmt.Errorf("failed to create postgres provider: %w", err)
	}

	// Per-cluster Grafana resources
	for _, release := range params.clusters {
		releaseName := fmt.Sprintf("%s-%s", name, release)
		grafanaRoleName := fmt.Sprintf("grafana-%s-%s", name, release)
		grafanaDBName := grafanaRoleName

		// Random password
		grafanaPw, err := random.NewRandomPassword(ctx, fmt.Sprintf("%s-db-grafana-pw", releaseName), &random.RandomPasswordArgs{
			Special:         pulumi.Bool(true),
			OverrideSpecial: pulumi.String("-_"),
			Length:          pulumi.Int(36),
		}, withClusterAlias())
		if err != nil {
			return fmt.Errorf("failed to create grafana password for %s: %w", release, err)
		}

		// Grafana role
		grafanaRole, err := postgresql.NewRole(ctx, fmt.Sprintf("%s-grafana-role", releaseName), &postgresql.RoleArgs{
			Login:    pulumi.Bool(true),
			Name:     pulumi.String(grafanaRoleName),
			Password: grafanaPw.Result,
		}, pulumi.Provider(pgProvider), withClusterAlias())
		if err != nil {
			return fmt.Errorf("failed to create grafana role for %s: %w", release, err)
		}

		// Grafana database
		grafanaDb, err := postgresql.NewDatabase(ctx, fmt.Sprintf("%s-grafana-db", releaseName), &postgresql.DatabaseArgs{
			Name:  pulumi.String(grafanaDBName),
			Owner: pulumi.String(grafanaRoleName),
		}, pulumi.Provider(pgProvider), pulumi.DependsOn([]pulumi.Resource{grafanaRole}), pulumi.Protect(true), withClusterAlias())
		if err != nil {
			return fmt.Errorf("failed to create grafana database for %s: %w", release, err)
		}

		// Grafana grant
		_, err = postgresql.NewGrant(ctx, fmt.Sprintf("%s-grafana-grant", releaseName), &postgresql.GrantArgs{
			Database:   pulumi.String(grafanaDBName),
			Role:       pulumi.String(grafanaRoleName),
			Schema:     pulumi.String("public"),
			ObjectType: pulumi.String("schema"),
			Privileges: pulumi.StringArray{pulumi.String("CREATE"), pulumi.String("USAGE")},
		}, pulumi.Provider(pgProvider), pulumi.DependsOn([]pulumi.Resource{grafanaDb, grafanaRole}), withClusterAlias())
		if err != nil {
			return fmt.Errorf("failed to create grafana grant for %s: %w", release, err)
		}

		// Key Vault secret with credentials for this cluster's Grafana
		secretValue := pulumi.All(grafanaPw.Result, pulumi.String(grafanaRoleName), pulumi.String(grafanaDBName)).
			ApplyT(func(args []interface{}) (string, error) {
				data := map[string]string{
					"password": args[0].(string),
					"role":     args[1].(string),
					"database": args[2].(string),
				}
				bytes, err := json.Marshal(data)
				if err != nil {
					return "", err
				}
				return string(bytes), nil
			}).(pulumi.StringOutput)

		_, err = keyvault.NewSecret(ctx, fmt.Sprintf("%s-postgres-grafana-user", releaseName), &keyvault.SecretArgs{
			SecretName:        pulumi.String(fmt.Sprintf("%s-postgres-grafana-user", releaseName)),
			ResourceGroupName: pulumi.String(params.resourceGroupName),
			VaultName:         pulumi.String(params.vaultName),
			Properties: &keyvault.SecretPropertiesArgs{
				Value: secretValue,
			},
		}, pulumi.Protect(params.protectPersistentResources))
		if err != nil {
			return fmt.Errorf("failed to create Key Vault secret for %s: %w", release, err)
		}

		// Export password (last cluster wins, matching Python behavior)
		ctx.Export("db_grafana_pw", grafanaPw.Result)
	}

	return nil
}
