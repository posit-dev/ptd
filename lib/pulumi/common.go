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
	// IgnoreTags is the list of exact AWS tag keys the default AWS provider should
	// leave untouched on managed resources. AWS-only; empty for other clouds.
	IgnoreTags []string
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

// ignoreTagsConfigEntry is a single path-config key/value pair used to populate the
// AWS provider's ignoreTags.keys list.
type ignoreTagsConfigEntry struct {
	Path  string
	Value string
}

// awsIgnoreTagsConfig maps a flat list of tag keys to the ordered path-config entries
// (aws:ignoreTags.keys[i] -> key) that wire them into the default AWS provider's ignoreTags.
// Extracted as a pure function so the path construction can be unit-tested without a live stack.
func awsIgnoreTagsConfig(ignoreTags []string) []ignoreTagsConfigEntry {
	entries := make([]ignoreTagsConfigEntry, 0, len(ignoreTags))
	for i, key := range ignoreTags {
		entries = append(entries, ignoreTagsConfigEntry{
			Path:  fmt.Sprintf("aws:ignoreTags.keys[%d]", i),
			Value: key,
		})
	}
	return entries
}

// ConfigureStackRegion sets the appropriate region configuration for the stack based on cloud provider.
// For AWS, any ignoreTags keys are wired into the default provider's aws:ignoreTags.keys so the
// listed customer tag keys are never added or removed on managed resources.
func ConfigureStackRegion(ctx context.Context, stack auto.Stack, cloud string, region string, ignoreTags []string) error {
	switch cloud {
	case "aws":
		if err := stack.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: region}); err != nil {
			return fmt.Errorf("failed to set AWS region: %w", err)
		}
		// Stack() creates an ephemeral workspace per invocation (auto.NewLocalWorkspace
		// with an inline program and no WorkDir → a fresh temp dir), so the config
		// below is rebuilt from scratch every run and always reflects exactly the
		// current ignore_tags list. There are no stale keys[i] entries to clean up
		// when the list shrinks. (If this ever moves to a persistent WorkDir, the
		// aws:ignoreTags key would need to be removed before rewriting it.)
		for _, entry := range awsIgnoreTagsConfig(ignoreTags) {
			if err := stack.SetConfigWithOptions(ctx,
				entry.Path,
				auto.ConfigValue{Value: entry.Value},
				&auto.ConfigOptions{Path: true}); err != nil {
				return fmt.Errorf("failed to set AWS ignoreTags: %w", err)
			}
		}
	case "azure":
		if err := stack.SetConfig(ctx, "azure-native:location", auto.ConfigValue{Value: region}); err != nil {
			return fmt.Errorf("failed to set Azure location: %w", err)
		}
	}
	return nil
}
