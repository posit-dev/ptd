package verify

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

const keycloakHTTPTimeout = 30 * time.Second

// EnsureTestUser ensures a test user exists in Keycloak and credentials are in a Secret
func EnsureTestUser(ctx context.Context, env []string, siteName string, keycloakURL string, realm string, testUsername string) error {
	// Check if the vip-test-credentials secret already exists
	checkCmd := exec.CommandContext(ctx, "kubectl", "get", "secret", "vip-test-credentials",
		"-n", namespace, "--ignore-not-found", "-o", "jsonpath={.metadata.name}")
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
	token, err := getKeycloakAdminToken(ctx, keycloakURL, adminUser, adminPass)
	if err != nil {
		return fmt.Errorf("failed to get admin token: %w", err)
	}

	// Create test user with a randomly generated password
	username := testUsername
	password, err := generatePassword(32)
	if err != nil {
		return fmt.Errorf("failed to generate password: %w", err)
	}

	if err := createKeycloakUser(ctx, keycloakURL, realm, token, username, password); err != nil {
		return fmt.Errorf("failed to create test user: %w", err)
	}

	// Create the vip-test-credentials secret
	if err := createCredentialsSecret(ctx, env, username, password); err != nil {
		return fmt.Errorf("failed to create credentials secret: %w", err)
	}

	slog.Info("Test user created successfully", "username", username)
	return nil
}

// generatePassword generates a cryptographically random password of the given length.
func generatePassword(length int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		result[i] = chars[n.Int64()]
	}
	return string(result), nil
}

// getKeycloakAdminCreds retrieves Keycloak admin credentials from the secret
func getKeycloakAdminCreds(ctx context.Context, env []string, secretName string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "get", "secret", secretName,
		"-n", namespace,
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

	// Decode base64 values using stdlib (portable, no subprocess)
	usernameBytes, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("failed to decode username: %w", err)
	}

	passwordBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("failed to decode password: %w", err)
	}

	return string(usernameBytes), string(passwordBytes), nil
}

// getKeycloakAdminToken gets an admin access token from Keycloak's master realm.
// Admin tokens are always obtained from the master realm, regardless of the target realm.
func getKeycloakAdminToken(ctx context.Context, keycloakURL, username, password string) (string, error) {
	tokenURL := fmt.Sprintf("%s/realms/master/protocol/openid-connect/token", keycloakURL)

	data := url.Values{
		"grant_type": {"password"},
		"client_id":  {"admin-cli"},
		"username":   {username},
		"password":   {password},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: keycloakHTTPTimeout}
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

// createKeycloakUser creates a user in Keycloak, or resets their password if they already exist.
// Resetting the password when the user exists ensures the K8s secret (written after this call)
// always matches the actual Keycloak credentials.
func createKeycloakUser(ctx context.Context, keycloakURL, realm, token, username, password string) error {
	client := &http.Client{Timeout: keycloakHTTPTimeout}
	usersURL := fmt.Sprintf("%s/admin/realms/%s/users", keycloakURL, url.PathEscape(realm))

	// Check if user already exists; use url.Values to safely encode the username.
	params := url.Values{"username": {username}, "exact": {"true"}}
	searchURL := usersURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	searchResp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer searchResp.Body.Close()

	if searchResp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(searchResp.Body)
		var users []map[string]interface{}
		if err := json.Unmarshal(body, &users); err == nil && len(users) > 0 {
			slog.Info("User already exists in Keycloak, resetting password", "username", username)
			userID, _ := users[0]["id"].(string)
			return resetKeycloakUserPassword(ctx, keycloakURL, realm, token, userID, password, client)
		}
	}

	// Create user with password
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

	req, err = http.NewRequestWithContext(ctx, "POST", usersURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
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

// resetKeycloakUserPassword sets a user's password via the Keycloak admin API.
func resetKeycloakUserPassword(ctx context.Context, keycloakURL, realm, token, userID, password string, client *http.Client) error {
	resetURL := fmt.Sprintf("%s/admin/realms/%s/users/%s/reset-password", keycloakURL, url.PathEscape(realm), url.PathEscape(userID))
	payload := map[string]interface{}{
		"type":      "password",
		"value":     password,
		"temporary": false,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", resetURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reset password failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// createCredentialsSecret creates a K8s secret with test user credentials.
// Uses JSON marshalling to prevent injection, consistent with job.go.
func createCredentialsSecret(ctx context.Context, env []string, username, password string) error {
	secret := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]string{
			"name":      "vip-test-credentials",
			"namespace": namespace,
		},
		"type": "Opaque",
		"data": map[string]string{
			"username": base64.StdEncoding.EncodeToString([]byte(username)),
			"password": base64.StdEncoding.EncodeToString([]byte(password)),
		},
	}

	secretJSON, err := json.Marshal(secret)
	if err != nil {
		return fmt.Errorf("failed to marshal secret: %w", err)
	}

	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-", "-n", namespace)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(string(secretJSON))

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply secret failed: %s", string(output))
	}

	return nil
}
