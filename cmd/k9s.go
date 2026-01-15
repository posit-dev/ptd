package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rstudio/ptd/cmd/internal/legacy"
	awslib "github.com/rstudio/ptd/lib/aws"
	"github.com/rstudio/ptd/lib/azure"
	"github.com/rstudio/ptd/lib/types"
	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"
	"tailscale.com/client/local"
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

func listAvailableClusters(ctx context.Context, region string, credEnvVars map[string]string) []string {
	listCmd := exec.CommandContext(ctx, "aws", "eks", "list-clusters", "--region", region, "--output", "text", "--query", "clusters")

	// Set AWS credentials in the environment
	listCmd.Env = os.Environ()
	for k, v := range credEnvVars {
		listCmd.Env = append(listCmd.Env, k+"="+v)
	}

	output, err := listCmd.Output()
	if err != nil {
		slog.Debug("Failed to list available clusters", "error", err)
		return nil
	}

	clusterList := strings.TrimSpace(string(output))
	if clusterList == "" || clusterList == "None" {
		return nil
	}

	return strings.Fields(clusterList)
}

func addProxyToKubeconfig(kubeconfigPath string) error {
	// Read the existing kubeconfig
	content, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig: %w", err)
	}

	// Parse the YAML
	var config map[string]interface{}
	if err := yaml.Unmarshal(content, &config); err != nil {
		return fmt.Errorf("failed to parse kubeconfig YAML: %w", err)
	}

	// Add proxy-url to all clusters
	if clusters, ok := config["clusters"].([]interface{}); ok {
		for _, cluster := range clusters {
			if clusterMap, ok := cluster.(map[interface{}]interface{}); ok {
				if clusterInfo, ok := clusterMap["cluster"].(map[interface{}]interface{}); ok {
					clusterInfo["proxy-url"] = "socks5://localhost:1080"
				}
			}
		}
	}

	// Write the modified kubeconfig back
	modifiedContent, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal modified kubeconfig: %w", err)
	}

	if err := os.WriteFile(kubeconfigPath, modifiedContent, 0600); err != nil {
		return fmt.Errorf("failed to write modified kubeconfig: %w", err)
	}

	return nil
}

func setupKubeConfig(cmd *cobra.Command, t types.Target, creds types.Credentials) (string, error) {
	// Create a temporary kubeconfig file
	tempDir := os.TempDir()
	kubeconfigPath := filepath.Join(tempDir, fmt.Sprintf("kubeconfig-%s", t.HashName()))
	ctx := cmd.Context()

	if t.CloudProvider() == types.AWS {
		// For AWS EKS, the cluster name pattern depends on whether it's a control room or workload
		var clusterName string
		if t.ControlRoom() {
			// Control room clusters follow the pattern: main01-{environment}
			// Extract environment from target name (e.g., "main01-staging" -> "staging")
			targetName := t.Name()
			if lastDash := strings.LastIndex(targetName, "-"); lastDash != -1 {
				environment := targetName[lastDash+1:]
				clusterName = fmt.Sprintf("main01-%s", environment)
			} else {
				// Fallback if no dash found
				clusterName = fmt.Sprintf("main01-%s", targetName)
			}
		} else {
			// Workload clusters follow the pattern: {target_name}-{release}
			// For now, we'll try to determine the release from the cluster configuration
			// or use "main" as default if we can't determine it
			awsTarget := t.(awslib.Target)

			// Try to get the first cluster name from the target's clusters map
			targetName := t.Name()
			slog.Debug("Workload target cluster info", "target", targetName, "clusters_count", len(awsTarget.Clusters))

			if len(awsTarget.Clusters) > 0 {
				// Construct cluster name from target name and release key
				// Pattern: {target_name}-{release}
				for releaseKey := range awsTarget.Clusters {
					slog.Debug("Found cluster release", "release_key", releaseKey)
					clusterName = fmt.Sprintf("%s-%s", targetName, releaseKey)
					break
				}
			}

			// If no cluster name found, use fallback
			if clusterName == "" {
				slog.Debug("No cluster name found, using fallback")
				clusterName = fmt.Sprintf("%s-main", targetName)
			}
		}

		// Set up AWS environment variables for the update-kubeconfig command
		credEnvVars := creds.EnvVars()

		// Log the cluster name and credentials info for debugging
		slog.Info("Setting up kubeconfig", "cluster_name", clusterName, "region", t.Region(), "target", t.Name())
		slog.Debug("AWS credentials info", "identity", creds.Identity(), "account_id", credEnvVars["AWS_ACCOUNT_ID"])
		// Build command arguments properly
		args := []string{"--region", t.Region()}
		if cmd.Flag("verbose").Value.String() == "true" {
			args = append(args, "--debug")
		}
		args = append(args, "eks", "update-kubeconfig", "--name", clusterName, "--kubeconfig", kubeconfigPath)

		updateCmd := exec.CommandContext(ctx, "aws", args...)

		// Set AWS credentials in the environment
		updateCmd.Env = os.Environ()
		for k, v := range credEnvVars {
			updateCmd.Env = append(updateCmd.Env, k+"="+v)
		}

		// Execute the update-kubeconfig command and capture both stdout and stderr
		var stdout, stderr strings.Builder
		updateCmd.Stdout = &stdout
		updateCmd.Stderr = &stderr

		// Build the exact command string for logging
		cmdArgs := []string{"aws"}
		cmdArgs = append(cmdArgs, args...)
		exactCommand := strings.Join(cmdArgs, " ")

		slog.Debug("Executing AWS command", "command", exactCommand)
		if err := updateCmd.Run(); err != nil {
			exitCode := -1
			if updateCmd.ProcessState != nil {
				exitCode = updateCmd.ProcessState.ExitCode()
			}

			slog.Error("AWS CLI command failed",
				"command", exactCommand,
				"exit_code", exitCode,
				"stderr", stderr.String(),
				"stdout", stdout.String(),
				"cluster_name", clusterName,
				"region", t.Region(),
				"identity", creds.Identity())

			// Try to list available clusters for debugging
			availableClusters := listAvailableClusters(ctx, t.Region(), credEnvVars)
			if len(availableClusters) > 0 {
				slog.Info("Available clusters in region", "region", t.Region(), "clusters", availableClusters)
			}

			return "", fmt.Errorf("failed to setup kubeconfig for cluster %s in region %s (exit code %d): %w\nCommand: %s\nAWS CLI stderr: %s", clusterName, t.Region(), exitCode, err, exactCommand, stderr.String())
		}

		// Add SOCKS proxy configuration for non-tailscale clusters
		if !t.TailscaleEnabled() {
			slog.Debug("Adding SOCKS proxy configuration to kubeconfig", "target", t.Name())
			if err := addProxyToKubeconfig(kubeconfigPath); err != nil {
				slog.Error("Failed to add proxy configuration to kubeconfig", "error", err)
				return "", fmt.Errorf("failed to add proxy configuration to kubeconfig: %w", err)
			}
		}

		return kubeconfigPath, nil
	}

	return "", fmt.Errorf("kubeconfig setup not implemented for cloud provider: %s", t.CloudProvider())
}

func runK9s(cmd *cobra.Command, target string) {
	// find the relevant ptd.yaml file, load it.
	t, err := legacy.TargetFromName(target)
	if err != nil {
		slog.Error("Could not load relevant ptd.yaml file", "error", err)
		return
	}

	if !t.TailscaleEnabled() {
		if t.CloudProvider() == types.AWS {
			ps := awslib.NewProxySession(t.(awslib.Target), getAwsCliPath(), "1080", proxyFile)
			err = ps.Start(cmd.Context())
			if err != nil {
				slog.Error("Error starting AWS proxy session", "error", err)
				return
			}
			defer func() {
				if stopErr := ps.Stop(); stopErr != nil {
					slog.Error("Failed to stop proxy session", "error", stopErr)
				}
			}()
		} else {
			ps := azure.NewProxySession(t.(azure.Target), getAzureCliPath(), "1080", proxyFile)
			err = ps.Start(cmd.Context())
			if err != nil {
				slog.Error("Error starting Azure proxy session", "error", err)
				return
			}
			defer func() {
				if stopErr := ps.Stop(); stopErr != nil {
					slog.Error("Failed to stop proxy session", "error", stopErr)
				}
			}()
		}
	} else {
		client := local.Client{}
		status, _ := client.Status(context.Background())
		isTailscaleConnected := status != nil && status.BackendState == "Running"

		if !isTailscaleConnected {
			slog.Warn("Tailscale is not connected, k9s connection may fail", "target_name", t.Name())
		}
	}

	creds, err := t.Credentials(cmd.Context())
	if err != nil {
		slog.Error("Failed to assume role", "error", err)
		return
	}

	// Set up kubeconfig
	kubeconfigPath, err := setupKubeConfig(cmd, t, creds)
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
