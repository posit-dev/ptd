package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
)

// EnsureTestUser ensures a test user exists in Keycloak and credentials are in a Secret
func EnsureTestUser(ctx context.Context, env []string, siteName string, keycloakURL string, realm string) error {
	// Check if the vip-test-credentials secret already exists
	checkCmd := exec.CommandContext(ctx, "kubectl", "get", "secret", "vip-test-credentials",
		"-n", "posit-team", "--ignore-not-found", "-o", "jsonpath={.metadata.name}")
	checkCmd.Env = env

	output, err := checkCmd.Output()
	if err == nil && strings.TrimSpace(string(output)) == "vip-test-credentials" {
		slog.Info("Test user credentials secret already exists, skipping creation")
		return nil
	}

	slog.Info("Creating test user in Keycloak")

	// Get Keycloak admin credentials from the Keycloak Operator's initial-admin secret
	// The Keycloak Operator creates this as {keycloak-cr-name}-initial-admin
	adminSecretName := fmt.Sprintf("%s-keycloak-initial-admin", siteName)
	adminUser, adminPass, err := getKeycloakAdminCreds(ctx, env, adminSecretName)
	if err != nil {
		return fmt.Errorf("failed to get Keycloak admin credentials: %w", err)
	}

	// Get admin access token
	token, err := getKeycloakAdminToken(keycloakURL, realm, adminUser, adminPass)
	if err != nil {
		return fmt.Errorf("failed to get admin token: %w", err)
	}

	// Create test user
	username := "vip-test-user"
	password := "vip-test-password-12345"

	if err := createKeycloakUser(keycloakURL, realm, token, username, password); err != nil {
		return fmt.Errorf("failed to create test user: %w", err)
	}

	// Create the vip-test-credentials secret
	if err := createCredentialsSecret(ctx, env, username, password); err != nil {
		return fmt.Errorf("failed to create credentials secret: %w", err)
	}

	slog.Info("Test user created successfully", "username", username)
	return nil
}

// getKeycloakAdminCreds retrieves Keycloak admin credentials from the secret
func getKeycloakAdminCreds(ctx context.Context, env []string, secretName string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "get", "secret", secretName,
		"-n", "posit-team",
		"-o", "jsonpath={.data.username} {.data.password}")
	cmd.Env = env

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", "", fmt.Errorf("kubectl get secret failed: %s", string(exitErr.Stderr))
		}
		return "", "", fmt.Errorf("kubectl get secret failed: %w", err)
	}

	parts := strings.Fields(string(output))
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected secret format")
	}

	// Decode base64 values
	userCmd := exec.CommandContext(ctx, "base64", "-d")
	userCmd.Stdin = strings.NewReader(parts[0])
	username, err := userCmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("failed to decode username: %w", err)
	}

	passCmd := exec.CommandContext(ctx, "base64", "-d")
	passCmd.Stdin = strings.NewReader(parts[1])
	password, err := passCmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("failed to decode password: %w", err)
	}

	return string(username), string(password), nil
}

// getKeycloakAdminToken gets an admin access token from Keycloak's master realm
func getKeycloakAdminToken(keycloakURL, _, username, password string) (string, error) {
	// Admin tokens are always obtained from the master realm
	tokenURL := fmt.Sprintf("%s/realms/master/protocol/openid-connect/token", keycloakURL)

	data := fmt.Sprintf("grant_type=password&client_id=admin-cli&username=%s&password=%s", username, password)
	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.AccessToken, nil
}

// createKeycloakUser creates a user in Keycloak
func createKeycloakUser(keycloakURL, realm, token, username, password string) error {
	usersURL := fmt.Sprintf("%s/admin/realms/%s/users", keycloakURL, realm)

	// Check if user already exists
	searchURL := fmt.Sprintf("%s?username=%s&exact=true", usersURL, username)
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		var users []map[string]interface{}
		if err := json.Unmarshal(body, &users); err == nil && len(users) > 0 {
			slog.Info("User already exists in Keycloak", "username", username)
			return nil
		}
	}

	// Create user
	userPayload := map[string]interface{}{
		"username":      username,
		"enabled":       true,
		"emailVerified": true,
		"credentials": []map[string]interface{}{
			{
				"type":      "password",
				"value":     password,
				"temporary": false,
			},
		},
	}

	payloadBytes, err := json.Marshal(userPayload)
	if err != nil {
		return err
	}

	req, err = http.NewRequest("POST", usersURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create user failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// createCredentialsSecret creates a K8s secret with test user credentials
func createCredentialsSecret(ctx context.Context, env []string, username, password string) error {
	cmd := exec.CommandContext(ctx, "kubectl", "create", "secret", "generic", "vip-test-credentials",
		"--from-literal=username="+username,
		"--from-literal=password="+password,
		"-n", "posit-team")
	cmd.Env = env

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl create secret failed: %s", string(output))
	}

	return nil
}
