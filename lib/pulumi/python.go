package pulumi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
	"github.com/spf13/viper"
)

func uvRuntime() workspace.ProjectRuntimeInfo {
	uv := map[string]interface{}{
		"toolchain":  "uv",
		"virtualenv": filepath.Join(viper.GetString("TOP"), "python-pulumi", ".venv"),
	}
	return workspace.NewProjectRuntimeInfo("python", uv)
}

func NewPythonPulumiStack(
	ctx context.Context,
	cloud string,
	targetType string,
	stackBaseName string,
	targetName string,
	targetRegion string,
	targetBackendUrl string,
	targetSecretsProviderKey string,
	extraEnvVars map[string]string,
	createAutoloadFile bool) (stack auto.Stack, err error) {

	projectName := buildProjectName(cloud, targetType, stackBaseName)

	stackName := auto.FullyQualifiedStackName("organization", projectName, targetName)
	secretsProvider := auto.SecretsProvider(targetSecretsProviderKey)
	stackSettings := auto.Stacks(map[string]workspace.ProjectStack{
		stackName: {SecretsProvider: targetSecretsProviderKey},
	})

	project := auto.Project(workspace.Project{
		Name:    tokens.PackageName(projectName),
		Runtime: uvRuntime(),
		Backend: &workspace.ProjectBackend{URL: targetBackendUrl},
	})

	envVars := k8sEnvVars()

	// Add the PTD_ROOT to the environment variables, to ensure python knows how to look up ptd.yaml locations
	envVars["PTD_ROOT"] = helpers.GetTargetsConfigPath()

	// Add the extra env vars to the environment variables
	// this will overwrite any existing env vars with the same name
	for k, v := range extraEnvVars {
		envVars[k] = v
	}

	lw, err := auto.NewLocalWorkspace(ctx, project, stackSettings, secretsProvider, auto.EnvVars(envVars))
	if err != nil {
		return
	}

	if createAutoloadFile {
		// inanely hacky attempt at making the class name title case, except for AWS.
		classCloud := helpers.TitleCase(cloud)
		if cloud == "aws" {
			classCloud = "AWS"
		}

		// this is some ridiculous shit right here.
		// we're building the module and class name to continue supporting the ptd/pulumi-python autoload() feature
		// without needing to check the files into source code.
		// the unicode word split algo also doesn't like "postgres_config", so just hack that in manually
		titleCaseStackBaseName := helpers.TitleCase(stackBaseName)
		if stackBaseName == "postgres_config" {
			titleCaseStackBaseName = "PostgresConfig"
		}
		if stackBaseName == "eks" {
			titleCaseStackBaseName = "EKS"
		}
		module := fmt.Sprintf("%s_%s_%s", cloud, strings.Replace(targetType, "-", "_", -1), stackBaseName)
		class := fmt.Sprintf("%s%s%s", classCloud, strings.Replace(helpers.TitleCase(targetType), "-", "", -1), titleCaseStackBaseName)

		err = WriteMainPy(lw.WorkDir(), module, class)
		if err != nil {
			return
		}
	}

	stack, err = auto.UpsertStack(ctx, stackName, lw)
	if err != nil {
		return
	}

	switch cloud {
	case "aws":
		if err := stack.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: targetRegion}); err != nil {
			return stack, err
		}
	case "azure":
		if err := stack.SetConfig(ctx, "azure-native:location", auto.ConfigValue{Value: targetRegion}); err != nil {
			return stack, err
		}
	}

	return

}

func WriteMainPy(dir string, module string, class string) error {
	contents := fmt.Sprintf("import ptd.pulumi_resources.%s\n\nptd.pulumi_resources.%s.%s.autoload()\n", module, module, class)
	mainPy := []byte(contents)
	return os.WriteFile(filepath.Join(dir, "__main__.py"), mainPy, 0644)
}
