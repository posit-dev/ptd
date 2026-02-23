package verify

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"gopkg.in/yaml.v3"
)

// Options contains configuration for the verify command
type Options struct {
	Target     string
	SiteName   string
	Categories string
	LocalMode  bool
	ConfigOnly bool
	Image      string
	Env        []string
}

// Run executes the VIP verification process
func Run(ctx context.Context, opts Options) error {
	slog.Info("Starting VIP verification", "target", opts.Target, "site", opts.SiteName)

	// Get Site CR from Kubernetes
	slog.Info("Fetching Site CR from Kubernetes")
	siteYAML, err := getSiteCR(ctx, opts.Env, opts.SiteName)
	if err != nil {
		return fmt.Errorf("failed to get Site CR: %w", err)
	}

	// Generate VIP config
	slog.Info("Generating VIP configuration")
	vipConfig, err := GenerateConfig(siteYAML, opts.Target)
	if err != nil {
		return fmt.Errorf("failed to generate VIP config: %w", err)
	}

	// If config-only mode, just print and exit
	if opts.ConfigOnly {
		fmt.Println(vipConfig)
		return nil
	}

	// Parse Site CR to get Keycloak URL
	var site SiteCR
	if err := parseYAML(siteYAML, &site); err != nil {
		return fmt.Errorf("failed to parse Site CR: %w", err)
	}

	keycloakURL := fmt.Sprintf("https://key.%s", site.Spec.Domain)

	// Ensure test user exists
	slog.Info("Ensuring test user exists in Keycloak")
	if err := EnsureTestUser(ctx, opts.Env, opts.SiteName, keycloakURL, "posit"); err != nil {
		return fmt.Errorf("failed to ensure test user: %w", err)
	}

	// Run tests based on mode
	if opts.LocalMode {
		return runLocalTests(ctx, vipConfig, opts.Categories)
	}

	return runKubernetesTests(ctx, opts, vipConfig)
}

// getSiteCR retrieves the Site CR YAML from Kubernetes
func getSiteCR(ctx context.Context, env []string, siteName string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "get", "site", siteName,
		"-n", "posit-team",
		"-o", "yaml")
	cmd.Env = env

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("kubectl get site failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("kubectl get site failed: %w", err)
	}

	return output, nil
}

// parseYAML is a helper to unmarshal YAML
func parseYAML(data []byte, v interface{}) error {
	// Use the yaml package imported in config.go
	// This is a simple wrapper to avoid importing yaml again
	var site SiteCR
	if err := yaml.Unmarshal(data, &site); err != nil {
		return err
	}
	if siteCR, ok := v.(*SiteCR); ok {
		*siteCR = site
	}
	return nil
}

// runLocalTests runs VIP tests locally using uv
func runLocalTests(ctx context.Context, vipConfig string, categories string) error {
	slog.Info("Running VIP tests locally")

	// Create temporary config file
	tmpfile, err := os.CreateTemp("", "vip-config-*.toml")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.WriteString(vipConfig); err != nil {
		tmpfile.Close()
		return fmt.Errorf("failed to write config: %w", err)
	}
	tmpfile.Close()

	// Run pytest with uv
	args := []string{"run", "pytest", "--config", tmpfile.Name(), "--tb=short", "-v"}
	if categories != "" {
		args = append(args, "-m", categories)
	}

	cmd := exec.CommandContext(ctx, "uv", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("VIP tests failed: %w", err)
	}

	fmt.Println("\nVIP tests completed successfully")
	return nil
}

// runKubernetesTests runs VIP tests as a Kubernetes Job
func runKubernetesTests(ctx context.Context, opts Options, vipConfig string) error {
	timestamp := time.Now().Format("20060102150405")
	jobName := fmt.Sprintf("vip-verify-%s", timestamp)
	configName := "vip-verify-config"

	slog.Info("Creating ConfigMap", "name", configName)
	if err := CreateConfigMap(ctx, opts.Env, configName, vipConfig); err != nil {
		return fmt.Errorf("failed to create ConfigMap: %w", err)
	}

	// Clean up ConfigMap on exit
	defer func() {
		slog.Debug("Cleaning up resources")
		if err := Cleanup(ctx, opts.Env, jobName, configName); err != nil {
			slog.Warn("Failed to cleanup resources", "error", err)
		}
	}()

	slog.Info("Creating VIP verification Job", "name", jobName)
	jobOpts := JobOptions{
		Image:      opts.Image,
		Categories: opts.Categories,
		JobName:    jobName,
		ConfigName: configName,
	}

	if err := CreateJob(ctx, opts.Env, jobOpts); err != nil {
		return fmt.Errorf("failed to create Job: %w", err)
	}

	slog.Info("Streaming Job logs")
	if err := StreamLogs(ctx, opts.Env, jobName); err != nil {
		slog.Warn("Failed to stream logs", "error", err)
	}

	slog.Info("Waiting for Job to complete")
	success, err := WaitForJob(ctx, opts.Env, jobName)
	if err != nil {
		return fmt.Errorf("failed to wait for Job: %w", err)
	}

	if !success {
		fmt.Println("\nVIP verification failed")
		return fmt.Errorf("VIP tests failed")
	}

	fmt.Println("\nVIP verification completed successfully")
	return nil
}
