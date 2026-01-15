package main

import (
	"log/slog"
	"os"
	"os/exec"

	"github.com/rstudio/ptd/cmd/internal/legacy"
	"github.com/rstudio/ptd/lib/pulumi"
	"github.com/rstudio/ptd/lib/steps"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(workonCmd)
}

var workonCmd = &cobra.Command{
	Use:   "workon <cluster> [step]",
	Short: "Work on a target workload",
	Long:  `Work on a target workload, optionally specifying a particular step (stack).`,
	Args:  cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, step := args[0], ""

		if len(args) == 2 {
			step = args[1]
		}

		runWorkOn(cmd, cluster, step)
	},
	ValidArgsFunction: legacy.ValidTargetArgs,
}

func runWorkOn(cmd *cobra.Command, target string, step string) {
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

	// get shell so we can at least try to make the experience kinda normal for any user.
	sh := os.Getenv("SHELL")
	if sh == "" {
		slog.Error("SHELL environment variable is not set")
		return
	}

	// create a new interactive subshell
	shellCommand := exec.Command(sh, "-i")

	// attach the standard input/output/error to the current process
	shellCommand.Stdout = os.Stdout
	shellCommand.Stderr = os.Stderr
	shellCommand.Stdin = os.Stdin

	// set the environment variables for the shell command, including the cred env vars
	shellCommand.Env = os.Environ()
	for k, v := range credEnvVars {
		shellCommand.Env = append(shellCommand.Env, k+"="+v)
	}

	// If a step is provided, create/load the Pulumi stack for that step
	if step != "" {
		if !steps.ValidStep(step, t.ControlRoom()) {
			slog.Error("Invalid step provided", "step", step)
			return
		}

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

		// set the command's working directory to the stack's workdir
		shellCommand.Dir = stack.Workspace().WorkDir()
	}

	cmd.Printf("Starting interactive shell in %s with session identity %s\n", shellCommand.Dir, creds.Identity())
	cmd.Printf("To exit the shell, type 'exit' or press Ctrl+D\n")

	// run the shell command
	err = shellCommand.Run()
	if err != nil {
		slog.Error("Failed to start interactive shell", "error", err)
		return
	}
}
