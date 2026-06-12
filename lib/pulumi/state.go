package pulumi

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
)

// stateWorkspaceConfig builds a StackConfig for a Go-runtime workspace used purely
// for reading state (stack outputs) or running manual state operations. The
// backend URL and secrets provider must match the values the stack was created
// with by ptd ensure, or output decryption will fail. Region is deliberately not
// set: state-only operations never invoke a provider, so ConfigureStackRegion is
// not called for these workspaces (unlike InlineStack/LocalStack).
func stateWorkspaceConfig(cloud, targetType, stackBaseName, targetName, backendURL, secretsProvider string, extraEnvVars map[string]string) StackConfig {
	envVars := k8sEnvVars()
	for k, v := range extraEnvVars {
		envVars[k] = v
	}
	return StackConfig{
		Cloud:           cloud,
		TargetType:      targetType,
		StackBaseName:   stackBaseName,
		TargetName:      targetName,
		BackendURL:      backendURL,
		SecretsProvider: secretsProvider,
		EnvVars:         envVars,
	}
}

// newStateStack creates (or fetches) a Go-runtime Pulumi stack handle suitable for
// state-only operations. It deliberately omits auto.Program(...) and plugin
// installation: reading outputs and running `pulumi stack export/import`,
// `state unprotect/delete` etc. need neither. The project, stack, backend, and
// secrets provider mirror exactly what ptd ensure uses for the same step so the
// state file and its secrets are read identically.
func newStateStack(ctx context.Context, config StackConfig) (auto.Stack, error) {
	slog.Debug("Creating Go-runtime state workspace",
		"project", config.ProjectName(),
		"stack", config.StackName(),
		"backend_url", config.BackendURL)

	wks, err := auto.NewLocalWorkspace(ctx,
		config.ProjectOption(),
		config.StackSettingsOption(),
		config.SecretsProviderOption(),
		config.EnvVarsOption(),
	)
	if err != nil {
		return auto.Stack{}, fmt.Errorf("failed to create Pulumi state workspace: %w", err)
	}

	stack, err := auto.UpsertStack(ctx, config.StackName(), wks)
	if err != nil {
		return auto.Stack{}, fmt.Errorf("failed to initialize stack %s: %w", config.StackName(), err)
	}

	return stack, nil
}

// ReadStackOutputs reads the outputs of an existing persistent/postgres_config (or
// any) stack using a Go-runtime workspace. It replaces the previous Python-runtime
// read path: no program runs, only the state file is read. The backend URL and
// secrets provider must match what ptd ensure used for the stack, otherwise
// encrypted outputs cannot be decrypted.
func ReadStackOutputs(
	ctx context.Context,
	cloud string,
	targetType string,
	stackBaseName string,
	targetName string,
	backendURL string,
	secretsProvider string,
	extraEnvVars map[string]string,
) (auto.OutputMap, error) {
	config := stateWorkspaceConfig(cloud, targetType, stackBaseName, targetName, backendURL, secretsProvider, extraEnvVars)
	stack, err := newStateStack(ctx, config)
	if err != nil {
		return nil, err
	}
	return stack.Outputs(ctx)
}

// NewStateWorkspaceStack builds a Go-runtime, state-capable stack handle for
// `ptd workon <target> <step>`. It exposes the workspace WorkDir so the calling
// shell can run manual `pulumi` commands (stack export/import, state
// unprotect/delete) against the right project/stack/backend. Like ReadStackOutputs
// it omits the program and plugin installation, which state operations do not need.
func NewStateWorkspaceStack(
	ctx context.Context,
	cloud string,
	targetType string,
	stackBaseName string,
	targetName string,
	backendURL string,
	secretsProvider string,
	extraEnvVars map[string]string,
) (auto.Stack, error) {
	config := stateWorkspaceConfig(cloud, targetType, stackBaseName, targetName, backendURL, secretsProvider, extraEnvVars)
	return newStateStack(ctx, config)
}
