package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"

	"github.com/posit-dev/ptd/cmd/internal"
	"github.com/posit-dev/ptd/cmd/internal/legacy"
	"github.com/posit-dev/ptd/lib/customization"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/kube"
	"github.com/posit-dev/ptd/lib/pulumi"
	"github.com/posit-dev/ptd/lib/steps"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(workonCmd)
}

var workonCmd = &cobra.Command{
	Use:   "workon <cluster> [step] [-- command [args...]]",
	Short: "Work on a target workload",
	Long: `Work on a target workload, optionally specifying a particular step (stack).

If -- is provided, runs the specified command with the workload's credentials
and kubeconfig configured, then exits with the command's exit code.
Without --, starts an interactive shell.`,
	Args: func(cmd *cobra.Command, args []string) error {
		dash := cmd.ArgsLenAtDash()
		if dash == -1 {
			// No --, normal behavior: 1 or 2 args
			if len(args) < 1 || len(args) > 2 {
				return fmt.Errorf("accepts between 1 and 2 arg(s), received %d", len(args))
			}
		} else {
			// Has --, validate pre-dash args and ensure command exists after --
			if dash < 1 || dash > 2 {
				return fmt.Errorf("accepts between 1 and 2 arg(s) before --, received %d", dash)
			}
			if len(args) <= dash {
				return fmt.Errorf("expected command after --")
			}
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		dash := cmd.ArgsLenAtDash()
		var cluster, step string
		var execCmd []string

		if dash == -1 {
			cluster = args[0]
			if len(args) == 2 {
				step = args[1]
			}
		} else {
			cluster = args[0]
			if dash == 2 {
				step = args[1]
			}
			execCmd = args[dash:]
		}

		runWorkOn(cmd, cluster, step, execCmd)
	},
	ValidArgsFunction: legacy.ValidTargetArgs,
}

func runWorkOn(cmd *cobra.Command, target string, step string, execCmd []string) {
	// find the relevant ptd.yaml file, load it.
	t, err := legacy.TargetFromName(target)
	if err != nil {
		slog.Error("Could not load relevant ptd.yaml file", "error", err)
		return
	}

	targetType := "workload"
	if t.ControlRoom() {
		targetType = "control-room"
	}

	creds, err := t.Credentials(cmd.Context())
	if err != nil {
		slog.Error("Failed to assume role", "error", err)
		return
	}

	credEnvVars := creds.EnvVars()

	// Start proxy if needed (non-fatal)
	proxyFile := path.Join(internal.DataDir(), "proxy.json")
	stopProxy, err := kube.StartProxy(cmd.Context(), t, proxyFile)
	if err != nil {
		slog.Warn("Failed to start proxy", "error", err)
	} else {
		defer stopProxy()
	}

	// Set up kubeconfig (non-fatal)
	kubeconfigPath, err := kube.SetupKubeConfig(cmd.Context(), t, creds)
	if err != nil {
		slog.Warn("Failed to setup kubeconfig, kubectl commands may not work", "error", err)
	}

	// Determine the working directory if a step is provided
	var workDir string
	var pulumiStackName string
	if step != "" {
		// Check if it's a custom step first
		yamlPath := helpers.YamlPathForTarget(t)
		workloadPath := filepath.Dir(yamlPath) // Get the directory, not the yaml file path
		customStep, isCustom := findCustomStep(workloadPath, step)

		if isCustom {
			// Handle custom step
			slog.Info("Working on custom step", "step", step, "path", customStep.Path)

			// Create stack based on local source defined in program path
			programPath := filepath.Join(workloadPath, "customizations", customStep.Path)
			stack, err := pulumi.LocalStack(
				cmd.Context(),
				t,
				programPath,
				step,
				credEnvVars,
			)
			if err != nil {
				slog.Error("Failed to create custom step stack", "error", err)
				return
			}

			workDir = programPath
			pulumiStackName = stack.Name()

		} else if steps.ValidStep(step, t.ControlRoom()) {
			// Handle standard step
			stack, err := pulumi.NewPythonPulumiStack(
				cmd.Context(),
				string(t.CloudProvider()), // ptd-<cloud>-<control-room/workload>-<stackname>
				targetType,
				step,
				t.Name(),
				t.Region(),
				t.PulumiBackendUrl(),
				t.PulumiSecretsProviderKey(),
				credEnvVars,
				true,
			)
			if err != nil {
				slog.Error("Failed to create Pulumi stack", "error", err)
				return
			}

			workDir = stack.Workspace().WorkDir()
		} else {
			slog.Error("Invalid step provided", "step", step)
			return
		}
	}

	// Command execution mode
	if len(execCmd) > 0 {
		shellCommand := exec.Command(execCmd[0], execCmd[1:]...)
		shellCommand.Stdout = os.Stdout
		shellCommand.Stderr = os.Stderr
		shellCommand.Stdin = os.Stdin
		shellCommand.Env = os.Environ()
		for k, v := range credEnvVars {
			shellCommand.Env = append(shellCommand.Env, k+"="+v)
		}
		if kubeconfigPath != "" {
			shellCommand.Env = append(shellCommand.Env, "KUBECONFIG="+kubeconfigPath)
		}

		// If a step is provided, set the working directory
		if workDir != "" {
			shellCommand.Dir = workDir
		}
		if pulumiStackName != "" {
			shellCommand.Env = append(shellCommand.Env, "PULUMI_STACK_NAME="+pulumiStackName)
		}

		err = shellCommand.Run()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Interactive mode (existing behavior)
	sh := os.Getenv("SHELL")
	if sh == "" {
		slog.Error("SHELL environment variable is not set")
		return
	}

	shellCommand := exec.Command(sh, "-i")
	shellCommand.Stdout = os.Stdout
	shellCommand.Stderr = os.Stderr
	shellCommand.Stdin = os.Stdin
	shellCommand.Env = os.Environ()
	for k, v := range credEnvVars {
		shellCommand.Env = append(shellCommand.Env, k+"="+v)
	}
	if kubeconfigPath != "" {
		shellCommand.Env = append(shellCommand.Env, "KUBECONFIG="+kubeconfigPath)
	}

	// If a step is provided, set the working directory and PULUMI_STACK_NAME
	if workDir != "" {
		shellCommand.Dir = workDir
	}
	if pulumiStackName != "" {
		shellCommand.Env = append(shellCommand.Env, "PULUMI_STACK_NAME="+pulumiStackName)
	}

	cmd.Printf("Starting interactive shell in %s with session identity %s\n", shellCommand.Dir, creds.Identity())
	cmd.Printf("To exit the shell, type 'exit' or press Ctrl+D\n")

	err = shellCommand.Run()
	if err != nil {
		slog.Error("Failed to start interactive shell", "error", err)
		return
	}
}

// findCustomStep checks if a step name corresponds to a custom step in the manifest
func findCustomStep(workloadPath string, stepName string) (*customization.CustomStep, bool) {
	manifestPath, found := customization.FindManifest(workloadPath)
	if !found {
		return nil, false
	}

	manifest, err := customization.LoadManifest(manifestPath)
	if err != nil {
		slog.Debug("Failed to load manifest", "manifestPath", manifestPath, "error", err)
		return nil, false
	}

	for _, cs := range manifest.CustomSteps {
		if cs.Name == stepName && cs.IsEnabled() {
			slog.Debug("Found matching custom step", "name", cs.Name, "path", cs.Path)
			return &cs, true
		}
	}

	return nil, false
}
