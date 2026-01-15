package pulumi

import (
	"context"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
)

// StackConfig encapsulates common configuration for creating Pulumi stacks
type StackConfig struct {
	Cloud           string
	TargetType      string
	StackBaseName   string
	TargetName      string
	TargetRegion    string
	BackendURL      string
	SecretsProvider string
	EnvVars         map[string]string
}

// buildProjectName creates a standardized project name
func buildProjectName(cloud string, targetType string, stackBaseName string) string {
	return fmt.Sprintf("ptd-%s-%s-%s", cloud, targetType, strings.Replace(stackBaseName, "_", "-", -1))
}

// ProjectName returns the formatted project name for this stack
func (c *StackConfig) ProjectName() string {
	return buildProjectName(c.Cloud, c.TargetType, c.StackBaseName)
}

// StackName returns the fully qualified stack name
func (c *StackConfig) StackName() string {
	return auto.FullyQualifiedStackName("organization", c.ProjectName(), c.TargetName)
}

// ProjectOption returns the LocalWorkspaceOption for configuring the project
func (c *StackConfig) ProjectOption() auto.LocalWorkspaceOption {
	return auto.Project(workspace.Project{
		Name:    tokens.PackageName(c.ProjectName()),
		Runtime: workspace.NewProjectRuntimeInfo("go", nil),
		Backend: &workspace.ProjectBackend{URL: c.BackendURL},
	})
}

// StackSettingsOption returns the LocalWorkspaceOption for stack settings
func (c *StackConfig) StackSettingsOption() auto.LocalWorkspaceOption {
	return auto.Stacks(map[string]workspace.ProjectStack{
		c.StackName(): {SecretsProvider: c.SecretsProvider},
	})
}

// SecretsProviderOption returns the LocalWorkspaceOption for secrets provider
func (c *StackConfig) SecretsProviderOption() auto.LocalWorkspaceOption {
	return auto.SecretsProvider(c.SecretsProvider)
}

// EnvVarsOption returns the LocalWorkspaceOption for environment variables
func (c *StackConfig) EnvVarsOption() auto.LocalWorkspaceOption {
	return auto.EnvVars(c.EnvVars)
}

// ConfigureStackRegion sets the appropriate region configuration for the stack based on cloud provider
func ConfigureStackRegion(ctx context.Context, stack auto.Stack, cloud string, region string) error {
	switch cloud {
	case "aws":
		if err := stack.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: region}); err != nil {
			return fmt.Errorf("failed to set AWS region: %w", err)
		}
	case "azure":
		if err := stack.SetConfig(ctx, "azure-native:location", auto.ConfigValue{Value: region}); err != nil {
			return fmt.Errorf("failed to set Azure location: %w", err)
		}
	}
	return nil
}
