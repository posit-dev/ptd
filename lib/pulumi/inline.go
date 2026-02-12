package pulumi

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/posit-dev/ptd/lib/types"
)

// Plugins required for the Pulumi stack. These should possibly be moved to a
// manifest, but this is fine for now IMO.
var RequiredPlugins = map[string]string{
	"aws":          "6.65.0",
	"azure-native": "3.8.0",
}

// Stack creates or fetches a Pulumi stack for the given target and base name (step)
func Stack(ctx context.Context, baseName string, target types.Target, program func(*pulumi.Context, types.Target) error, envs map[string]string) (auto.Stack, error) {
	allEnvVars := k8sEnvVars()
	for k, v := range envs {
		allEnvVars[k] = v
	}

	config := StackConfig{
		Cloud:           string(target.CloudProvider()),
		TargetType:      string(target.Type()),
		StackBaseName:   baseName,
		TargetName:      target.Name(),
		TargetRegion:    target.Region(),
		BackendURL:      target.PulumiBackendUrl(),
		SecretsProvider: target.PulumiSecretsProviderKey(),
		EnvVars:         allEnvVars,
	}

	slog.Info("Pulumi stack", "project", config.ProjectName(), "stack", config.StackName())

	// Initialize the Pulumi workspace with the project and stack settings
	wks, err := auto.NewLocalWorkspace(ctx,
		config.ProjectOption(),
		config.StackSettingsOption(),
		config.SecretsProviderOption(),
		auto.Program(func(ctx *pulumi.Context) error {
			return program(ctx, target)
		}),
		config.EnvVarsOption(),
	)
	if err != nil {
		return auto.Stack{}, fmt.Errorf("failed to create Pulumi workspace: %w", err)
	}

	// Install all the necessary plugins
	for plugin, version := range RequiredPlugins {
		err = wks.InstallPlugin(ctx, plugin, version)
		if err != nil {
			return auto.Stack{}, fmt.Errorf("failed to install plugin %s: %w", plugin, err)
		}
	}

	stack, err := auto.UpsertStack(ctx, config.StackName(), wks)
	if err != nil {
		return auto.Stack{}, fmt.Errorf("failed to initialize stack %s: %w", config.StackName(), err)
	}

	if err := ConfigureStackRegion(ctx, stack, config.Cloud, config.TargetRegion); err != nil {
		return auto.Stack{}, err
	}

	return stack, nil
}
