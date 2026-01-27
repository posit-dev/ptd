package pulumi

import (
	"context"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
)

// LocalStack creates a new Go Pulumi stack based on local source files with remote state backend
// Returns the stack and an error
func LocalStack(
	ctx context.Context,
	target types.Target,
	programPath string, // Path to the Go Pulumi program directory
	stackBaseName string,
	extraEnvVars map[string]string,
) (stack auto.Stack, err error) {
	envVars := k8sEnvVars()
	for k, v := range extraEnvVars {
		envVars[k] = v
	}

	targetType := "workload"
	if target.ControlRoom() {
		targetType = "control-room"
	}

	config := StackConfig{
		Cloud:           string(target.CloudProvider()),
		TargetType:      targetType,
		StackBaseName:   stackBaseName,
		TargetName:      target.Name(),
		TargetRegion:    target.Region(),
		BackendURL:      target.PulumiBackendUrl(),
		SecretsProvider: target.PulumiSecretsProviderKey(),
		EnvVars:         envVars,
	}

	lw, err := auto.NewLocalWorkspace(ctx,
		config.ProjectOption(),
		config.StackSettingsOption(),
		config.SecretsProviderOption(),
		auto.WorkDir(programPath), // Use temp directory instead of programPath
		config.EnvVarsOption(),
	)
	if err != nil {
		return stack, err
	}

	stack, err = auto.UpsertStack(ctx, config.StackName(), lw)
	if err != nil {
		return stack, err
	}

	if err := ConfigureStackRegion(ctx, stack, config.Cloud, config.TargetRegion); err != nil {
		return stack, err
	}

	return stack, nil
}
