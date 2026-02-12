package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"

	"github.com/posit-dev/ptd/cmd/internal"
	"github.com/posit-dev/ptd/cmd/internal/legacy"
	"github.com/posit-dev/ptd/lib/kube"
	"github.com/spf13/cobra"
)

var namespace string
var extraArgs []string

func init() {
	k9sCmd.Flags().StringVarP(&namespace, "namespace", "n", "posit-team", "Namespace to focus on")
	k9sCmd.Flags().StringArrayVar(&extraArgs, "args", []string{}, "Additional arguments to pass to k9s")
	rootCmd.AddCommand(k9sCmd)
}

var k9sCmd = &cobra.Command{
	Use:   "k9s <cluster>",
	Short: "Run k9s on a target cluster.",
	Long:  `Run k9s on a target cluster.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster := args[0]
		runK9s(cmd, cluster)
	},
	ValidArgsFunction: legacy.ValidTargetArgs,
}

func runK9s(cmd *cobra.Command, target string) {
	t, err := legacy.TargetFromName(target)
	if err != nil {
		slog.Error("Could not load relevant ptd.yaml file", "error", err)
		return
	}

	// Start proxy using shared kube package
	proxyFile := path.Join(internal.DataDir(), "proxy.json")
	stopProxy, err := kube.StartProxy(cmd.Context(), t, proxyFile)
	if err != nil {
		slog.Error("Error starting proxy session", "error", err)
		return
	}
	defer stopProxy()

	creds, err := t.Credentials(cmd.Context())
	if err != nil {
		slog.Error("Failed to assume role", "error", err)
		return
	}

	// Set up kubeconfig using native SDK
	kubeconfigPath, err := kube.SetupKubeConfig(cmd.Context(), t, creds)
	if err != nil {
		slog.Error("Failed to setup kubeconfig", "error", err)
		return
	}

	credEnvVars := creds.EnvVars()

	sh := os.Getenv("SHELL")
	if sh == "" {
		slog.Error("SHELL environment variable is not set")
		return
	}

	k9sCommand := "k9s"
	if namespace != "" {
		k9sCommand += fmt.Sprintf(" -n %s", namespace)
	}
	for _, arg := range extraArgs {
		k9sCommand += " " + arg
	}
	shellCommand := exec.Command(sh, "-c", k9sCommand)

	// attach the standard input/output/error to the current process
	shellCommand.Stdout = os.Stdout
	shellCommand.Stderr = os.Stderr
	shellCommand.Stdin = os.Stdin

	// set the environment variables for the shell command, including the cred env vars and kubeconfig
	shellCommand.Env = os.Environ()
	for k, v := range credEnvVars {
		shellCommand.Env = append(shellCommand.Env, k+"="+v)
	}
	shellCommand.Env = append(shellCommand.Env, "KUBECONFIG="+kubeconfigPath)

	cmd.Printf("Starting k9s with session identity %s\n", creds.Identity())

	// run the shell command
	err = shellCommand.Run()
	if err != nil {
		slog.Error("Failed to start k9s", "error", err)
		return
	}
}
