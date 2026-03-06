package verify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Options contains configuration for the verify command
type Options struct {
	Target              string
	SiteName            string
	Namespace           string        // Kubernetes namespace (default: posit-team)
	Categories          string
	LocalMode           bool
	ConfigOnly          bool
	Image               string
	KeycloakURL         string        // overrides the default https://key.<domain> if set
	Realm               string        // Keycloak realm (default: posit)
	TestUsername        string        // Keycloak test user name (default: vip-test-user)
	KeycloakAdminSecret string        // overrides the default {siteName}-keycloak-initial-admin
	InteractiveAuth     bool          // mint credentials via interactive browser login
	Timeout             time.Duration // WaitForJob timeout (default: 15 minutes)
	Env                 []string
}

// Run executes the VIP verification process
func Run(ctx context.Context, opts Options) error {
	slog.Info("Starting VIP verification", "target", opts.Target, "site", opts.SiteName)

	// Get Site CR from Kubernetes
	slog.Info("Fetching Site CR from Kubernetes")
	siteYAML, err := getSiteCR(ctx, opts.Env, opts.SiteName, opts.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get Site CR: %w", err)
	}

	// Parse Site CR once
	var site SiteCR
	if err := yaml.Unmarshal(siteYAML, &site); err != nil {
		return fmt.Errorf("failed to parse Site CR: %w", err)
	}

	// Generate VIP config
	slog.Info("Generating VIP configuration")
	vipConfig, err := GenerateConfig(&site, opts.Target)
	if err != nil {
		return fmt.Errorf("failed to generate VIP config: %w", err)
	}

	// If config-only mode, just print and exit
	if opts.ConfigOnly {
		fmt.Println(vipConfig)
		return nil
	}

	// Handle credentials based on authentication mode
	var credentialsAvailable bool
	if opts.InteractiveAuth {
		// Interactive auth mode: mint credentials via VIP CLI and kubectl exec
		slog.Info("Using interactive authentication mode")

		// Get Connect URL from site
		connectURL := ""
		if site.Spec.Connect != nil {
			connectURL = buildProductURL(site.Spec.Connect, "connect", site.Spec.Domain)
		}
		if connectURL == "" {
			return fmt.Errorf("Connect is not configured for this site; interactive auth requires Connect")
		}

		// Mint Connect API key via VIP CLI
		slog.Info("Minting Connect API key via VIP CLI")
		apiKey, keyName, err := MintConnectKey(ctx, connectURL)
		if err != nil {
			return fmt.Errorf("failed to mint Connect API key: %w", err)
		}

		// Generate Workbench token
		slog.Info("Generating Workbench API token")
		workbenchToken, err := GenerateWorkbenchToken(ctx, opts.Env, opts.Namespace, opts.SiteName, opts.TestUsername)
		if err != nil {
			return fmt.Errorf("failed to generate Workbench token: %w", err)
		}

		// Generate Package Manager token
		slog.Info("Generating Package Manager token")
		pmToken, err := GeneratePackageManagerToken(ctx, opts.Env, opts.Namespace, opts.SiteName)
		if err != nil {
			return fmt.Errorf("failed to generate Package Manager token: %w", err)
		}

		// Save all credentials to K8s Secret
		creds := map[string]string{
			"VIP_CONNECT_API_KEY":     apiKey,
			"VIP_CONNECT_KEY_NAME":    keyName,
			"VIP_WORKBENCH_API_KEY":   workbenchToken,
			"VIP_PM_TOKEN":            pmToken,
		}
		slog.Info("Saving credentials to Kubernetes Secret")
		if err := SaveCredentialsSecret(ctx, opts.Env, opts.Namespace, creds); err != nil {
			return fmt.Errorf("failed to save credentials secret: %w", err)
		}

		credentialsAvailable = true
	} else {
		// Keycloak mode: ensure test user exists
		credentialsAvailable = site.Spec.Keycloak != nil && site.Spec.Keycloak.Enabled
		keycloakURL, err := deriveKeycloakURL(opts.KeycloakURL, site.Spec.Domain, credentialsAvailable)
		if err != nil {
			return err
		}
		if credentialsAvailable {
			adminSecretName := opts.KeycloakAdminSecret
			if adminSecretName == "" {
				adminSecretName = fmt.Sprintf("%s-keycloak-initial-admin", opts.SiteName)
			}
			slog.Info("Ensuring test user exists in Keycloak")
			if err := EnsureTestUser(ctx, opts.Env, keycloakURL, opts.Realm, opts.TestUsername, adminSecretName, opts.Namespace); err != nil {
				return fmt.Errorf("failed to ensure test user: %w", err)
			}
		} else {
			slog.Info("Keycloak not configured for this site, skipping test user creation")
		}
	}

	// Run tests based on mode
	if opts.LocalMode {
		return runLocalTests(ctx, opts, vipConfig, credentialsAvailable)
	}

	return runKubernetesTests(ctx, opts, vipConfig, credentialsAvailable)
}

// getSiteCR retrieves the Site CR YAML from Kubernetes
func getSiteCR(ctx context.Context, env []string, siteName, namespace string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "get", "site", siteName,
		"-n", namespace,
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

// deriveKeycloakURL returns the Keycloak URL to use. If override is non-empty it is returned
// as-is. Otherwise the URL is derived from domain. An error is returned only when Keycloak is
// enabled (needsURL) and domain is empty, which would produce an invalid URL.
func deriveKeycloakURL(override, domain string, needsURL bool) (string, error) {
	if override != "" {
		return override, nil
	}
	if needsURL && domain == "" {
		return "", fmt.Errorf("site domain is required to derive Keycloak URL; use --keycloak-url to override")
	}
	if domain == "" {
		return "", nil
	}
	return fmt.Sprintf("https://key.%s", domain), nil
}

// runLocalTests runs VIP tests locally using uv
func runLocalTests(ctx context.Context, opts Options, vipConfig string, credentialsAvailable bool) error {
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
	if opts.Categories != "" {
		args = append(args, "-m", opts.Categories)
	}

	cmd := exec.CommandContext(ctx, "uv", args...)
	if credentialsAvailable {
		if opts.InteractiveAuth {
			// Interactive auth mode: fetch API tokens from Secret
			localEnv, err := buildLocalEnvWithTokens(ctx, opts.Env, opts.Namespace)
			if err != nil {
				return err
			}
			cmd.Env = localEnv
		} else {
			// Keycloak mode: fetch username/password from Secret
			testUser, testPass, err := getSecretCredentials(ctx, opts.Env, vipTestCredentialsSecret, opts.Namespace)
			if err != nil {
				return fmt.Errorf("failed to get test credentials: %w", err)
			}
			localEnv, err := buildLocalEnv(opts.Env, testUser, testPass)
			if err != nil {
				return err
			}
			cmd.Env = localEnv
		}
	} else {
		cmd.Env = opts.Env
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("VIP tests failed: %w", err)
	}

	fmt.Println("\nVIP tests completed successfully")
	return nil
}

// buildLocalEnv constructs the environment for a local uv invocation.
// It strips any pre-existing VIP_TEST_USERNAME/VIP_TEST_PASSWORD entries from env
// (preventing duplicates when the caller's environment already exports them),
// then appends the provided credentials. Returns an error if credentials contain
// newline characters.
func buildLocalEnv(env []string, testUser, testPass string) ([]string, error) {
	if strings.ContainsAny(testUser, "\n\r\x00") || strings.ContainsAny(testPass, "\n\r\x00") {
		return nil, fmt.Errorf("test credentials must not contain newline or null characters")
	}
	result := make([]string, 0, len(env)+2)
	for _, e := range env {
		if !strings.HasPrefix(e, "VIP_TEST_USERNAME=") && !strings.HasPrefix(e, "VIP_TEST_PASSWORD=") {
			result = append(result, e)
		}
	}
	return append(result, "VIP_TEST_USERNAME="+testUser, "VIP_TEST_PASSWORD="+testPass), nil
}

// buildLocalEnvWithTokens fetches API tokens from the K8s Secret and constructs
// the environment for local VIP runs with interactive auth.
func buildLocalEnvWithTokens(ctx context.Context, env []string, namespace string) ([]string, error) {
	// Get the Secret
	cmd := exec.CommandContext(ctx, "kubectl", "get", "secret", vipTestCredentialsSecret,
		"-n", namespace,
		"-o", "json")
	cmd.Env = env

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("kubectl get secret failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("kubectl get secret failed: %w", err)
	}

	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(output, &secret); err != nil {
		return nil, fmt.Errorf("failed to parse secret: %w", err)
	}

	// Decode and extract tokens
	tokenKeys := []string{"VIP_CONNECT_API_KEY", "VIP_WORKBENCH_API_KEY", "VIP_PM_TOKEN"}
	tokens := make(map[string]string)
	for _, key := range tokenKeys {
		if b64Value, ok := secret.Data[key]; ok {
			decoded, err := base64.StdEncoding.DecodeString(b64Value)
			if err != nil {
				return nil, fmt.Errorf("failed to decode %s: %w", key, err)
			}
			tokens[key] = string(decoded)
		}
	}

	// Strip any pre-existing token env vars from env
	result := make([]string, 0, len(env)+len(tokens))
	for _, e := range env {
		skip := false
		for key := range tokens {
			if strings.HasPrefix(e, key+"=") {
				skip = true
				break
			}
		}
		if !skip {
			result = append(result, e)
		}
	}

	// Append token env vars
	for key, value := range tokens {
		result = append(result, key+"="+value)
	}

	return result, nil
}

// randomHex returns n random hex-encoded bytes (2n hex characters).
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// runKubernetesTests runs VIP tests as a Kubernetes Job
func runKubernetesTests(ctx context.Context, opts Options, vipConfig string, credentialsAvailable bool) error {
	suffix, err := randomHex(3) // 6 hex chars
	if err != nil {
		return fmt.Errorf("failed to generate name suffix: %w", err)
	}
	timestamp := time.Now().Format("20060102150405")
	jobName := fmt.Sprintf("vip-verify-%s-%s", timestamp, suffix)
	configName := fmt.Sprintf("vip-verify-config-%s-%s", timestamp, suffix)

	slog.Info("Creating ConfigMap", "name", configName)
	if err := CreateConfigMap(ctx, opts.Env, configName, vipConfig, opts.Namespace); err != nil {
		return fmt.Errorf("failed to create ConfigMap: %w", err)
	}

	// Clean up resources on exit using a fresh context so cleanup succeeds
	// even if the caller context has expired after the job wait.
	defer func() {
		slog.Debug("Cleaning up resources")
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := Cleanup(cleanupCtx, opts.Env, jobName, configName, opts.Namespace); err != nil {
			slog.Warn("Failed to cleanup resources", "error", err)
		}
	}()

	slog.Info("Creating VIP verification Job", "name", jobName)
	jobOpts := JobOptions{
		Image:                opts.Image,
		Categories:           opts.Categories,
		JobName:              jobName,
		ConfigName:           configName,
		Namespace:            opts.Namespace,
		CredentialsAvailable: credentialsAvailable,
		InteractiveAuth:      opts.InteractiveAuth,
		Timeout:              opts.Timeout,
	}

	if err := CreateJob(ctx, opts.Env, jobOpts); err != nil {
		return fmt.Errorf("failed to create Job: %w", err)
	}

	slog.Info("Streaming Job logs")
	if err := StreamLogs(ctx, opts.Env, jobName, opts.Namespace, opts.Timeout); err != nil {
		if errors.Is(err, errImagePull) {
			return err
		}
		slog.Warn("Failed to stream logs", "error", err)
	}

	slog.Info("Waiting for Job to complete")
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 15 * time.Minute
	}
	success, err := WaitForJob(ctx, opts.Env, jobName, opts.Namespace, timeout)
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
