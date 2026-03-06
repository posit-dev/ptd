package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// MintConnectKey calls the VIP CLI to mint a Connect API key via interactive browser auth.
// Returns the API key and key name from the VIP CLI output.
func MintConnectKey(ctx context.Context, connectURL string) (apiKey string, keyName string, err error) {
	// Check if vip CLI is available
	if _, err := exec.LookPath("vip"); err != nil {
		return "", "", fmt.Errorf("VIP CLI not found. Install with: pip install /path/to/vip")
	}

	cmd := exec.CommandContext(ctx, "vip", "auth", "mint-connect-key", "--url", connectURL)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", "", fmt.Errorf("vip auth mint-connect-key failed: %s", string(exitErr.Stderr))
		}
		return "", "", fmt.Errorf("vip auth mint-connect-key failed: %w", err)
	}

	// Parse JSON output: {"api_key": "...", "key_name": "..."}
	var result struct {
		APIKey  string `json:"api_key"`
		KeyName string `json:"key_name"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", "", fmt.Errorf("failed to parse vip CLI output: %w", err)
	}

	if result.APIKey == "" || result.KeyName == "" {
		return "", "", fmt.Errorf("vip CLI returned empty api_key or key_name")
	}

	return result.APIKey, result.KeyName, nil
}

// GenerateWorkbenchToken generates a Workbench API token via kubectl exec.
func GenerateWorkbenchToken(ctx context.Context, env []string, namespace, siteName, username string) (string, error) {
	deploymentName := fmt.Sprintf("workbench-%s", siteName)
	cmd := exec.CommandContext(ctx, "kubectl", "exec",
		fmt.Sprintf("deploy/%s", deploymentName),
		"-n", namespace,
		"--",
		"rstudio-server", "generate-api-token", "user", "vip-test", username)
	cmd.Env = env

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("kubectl exec generate-api-token failed: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("kubectl exec generate-api-token failed: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// GeneratePackageManagerToken generates a PM token via kubectl exec.
func GeneratePackageManagerToken(ctx context.Context, env []string, namespace, siteName string) (string, error) {
	deploymentName := fmt.Sprintf("package-manager-%s", siteName)
	cmd := exec.CommandContext(ctx, "kubectl", "exec",
		fmt.Sprintf("deploy/%s", deploymentName),
		"-n", namespace,
		"--",
		"rspm", "create", "token", "--scope=repos:read", "--quiet")
	cmd.Env = env

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("kubectl exec rspm create token failed: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("kubectl exec rspm create token failed: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// SaveCredentialsSecret creates or updates the vip-test-credentials K8s Secret with all tokens.
func SaveCredentialsSecret(ctx context.Context, env []string, namespace string, creds map[string]string) error {
	// Build kubectl create secret generic command
	args := []string{"create", "secret", "generic", vipTestCredentialsSecret, "-n", namespace}
	for key, value := range creds {
		args = append(args, fmt.Sprintf("--from-literal=%s=%s", key, value))
	}
	args = append(args, "--dry-run=client", "-o", "json")

	// Generate the secret manifest
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Env = env
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("kubectl create secret failed: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("kubectl create secret failed: %w", err)
	}

	// Parse the generated secret to add labels
	var secret map[string]interface{}
	if err := json.Unmarshal(output, &secret); err != nil {
		return fmt.Errorf("failed to parse secret manifest: %w", err)
	}

	// Add labels
	metadata := secret["metadata"].(map[string]interface{})
	metadata["labels"] = map[string]string{
		"app.kubernetes.io/managed-by": "ptd",
		"app.kubernetes.io/name":       "vip-verify",
	}

	// Marshal back to JSON
	secretJSON, err := json.Marshal(secret)
	if err != nil {
		return fmt.Errorf("failed to marshal secret: %w", err)
	}

	// Apply the secret
	applyCmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-", "-n", namespace)
	applyCmd.Env = env
	applyCmd.Stdin = strings.NewReader(string(secretJSON))

	if output, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply secret failed: %s", string(output))
	}

	return nil
}
