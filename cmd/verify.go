package main

import (
	"context"
	"log/slog"
	"os"
	"path"
	"strings"

	"github.com/posit-dev/ptd/cmd/internal"
	"github.com/posit-dev/ptd/cmd/internal/legacy"
	"github.com/posit-dev/ptd/cmd/internal/verify"
	"github.com/posit-dev/ptd/lib/kube"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(verifyCmd)

	verifyCmd.Flags().StringVar(&verifySiteName, "site", "main", "Name of the Site CR to verify")
	verifyCmd.Flags().StringVar(&verifyCategories, "categories", "", "Test categories to run (pytest -m marker)")
	verifyCmd.Flags().BoolVar(&verifyLocal, "local", false, "Run tests locally instead of in Kubernetes")
	verifyCmd.Flags().BoolVar(&verifyConfigOnly, "config-only", false, "Generate config only, don't run tests")
	verifyCmd.Flags().StringVar(&verifyImage, "image", "ghcr.io/posit-dev/vip:latest", "VIP container image to use")
	verifyCmd.Flags().StringVar(&verifyKeycloakURL, "keycloak-url", "", "Keycloak URL (defaults to https://key.<domain> from Site CR)")
}

var (
	verifySiteName    string
	verifyCategories  string
	verifyLocal       bool
	verifyConfigOnly  bool
	verifyImage       string
	verifyKeycloakURL string
)

var verifyCmd = &cobra.Command{
	Use:   "verify <target>",
	Short: "Verify a PTD deployment with VIP tests",
	Long: `Verify a PTD deployment by running VIP (Verified Installation of Posit) tests.

This command:
1. Fetches the Site CR from Kubernetes
2. Generates a VIP configuration from the Site CR
3. Ensures a test user exists in Keycloak
4. Runs VIP tests either locally or as a Kubernetes Job

Examples:
  # Run all VIP tests against ganso01-staging
  ptd verify ganso01-staging

  # Run only smoke tests
  ptd verify ganso01-staging --categories smoke

  # Generate config only without running tests
  ptd verify ganso01-staging --config-only

  # Run tests locally instead of in Kubernetes
  ptd verify ganso01-staging --local

  # Verify a specific site (for multi-site deployments)
  ptd verify ganso01-staging --site secondary`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: legacy.ValidTargetArgs,
	Run: func(cmd *cobra.Command, args []string) {
		target := args[0]
		runVerify(cmd.Context(), cmd, target)
	},
}

func runVerify(ctx context.Context, cmd *cobra.Command, target string) {
	// Load target configuration
	t, err := legacy.TargetFromName(target)
	if err != nil {
		slog.Error("Could not load target", "error", err)
		os.Exit(1)
	}

	// Get credentials
	creds, err := t.Credentials(ctx)
	if err != nil {
		slog.Error("Failed to get credentials", "error", err)
		os.Exit(1)
	}

	credEnvVars := creds.EnvVars()

	// Start proxy if needed (non-fatal)
	proxyFile := path.Join(internal.DataDir(), "proxy.json")
	stopProxy, err := kube.StartProxy(ctx, t, proxyFile)
	if err != nil {
		slog.Warn("Failed to start proxy", "error", err)
	} else {
		defer stopProxy()
	}

	// Set up kubeconfig
	kubeconfigPath, err := kube.SetupKubeConfig(ctx, t, creds)
	if err != nil {
		slog.Error("Failed to setup kubeconfig", "error", err)
		os.Exit(1)
	}

	if strings.HasSuffix(verifyImage, ":latest") {
		slog.Warn("Using ':latest' image tag is non-deterministic; consider pinning a specific version", "image", verifyImage)
	}

	// Prepare environment variables for kubectl (inherit from current env)
	env := os.Environ()
	for k, v := range credEnvVars {
		env = append(env, k+"="+v)
	}
	env = append(env, "KUBECONFIG="+kubeconfigPath)

	// Run verification
	opts := verify.Options{
		Target:       target,
		SiteName:     verifySiteName,
		Categories:   verifyCategories,
		LocalMode:    verifyLocal,
		ConfigOnly:   verifyConfigOnly,
		Image:        verifyImage,
		KeycloakURL:  verifyKeycloakURL,
		TestUsername: "vip-test-user",
		Env:          env,
	}

	if err := verify.Run(ctx, opts); err != nil {
		slog.Error("Verification failed", "error", err)
		os.Exit(1)
	}
}
