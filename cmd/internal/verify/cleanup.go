package verify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CleanupCredentials deletes VIP test credentials and resources.
func CleanupCredentials(ctx context.Context, env []string, namespace, connectURL string) error {
	// Read vip-test-credentials Secret to get the Connect API key and key name
	cmd := exec.CommandContext(ctx, "kubectl", "get", "secret", vipTestCredentialsSecret,
		"-n", namespace,
		"-o", "json")
	cmd.Env = env

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Secret doesn't exist, nothing to clean up
			if strings.Contains(string(exitErr.Stderr), "not found") {
				fmt.Fprintf(os.Stderr, "No credentials secret found, nothing to clean up\n")
				return nil
			}
			return fmt.Errorf("failed to get credentials secret: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("failed to get credentials secret: %w", err)
	}

	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(output, &secret); err != nil {
		return fmt.Errorf("failed to parse secret: %w", err)
	}

	// Extract and decode the Connect API key and key name
	apiKeyB64, hasAPIKey := secret.Data["VIP_CONNECT_API_KEY"]
	keyNameB64, hasKeyName := secret.Data["VIP_CONNECT_KEY_NAME"]

	if hasAPIKey && hasKeyName && connectURL != "" {
		// Decode base64 values
		apiKeyBytes, err := base64.StdEncoding.DecodeString(apiKeyB64)
		if err != nil {
			return fmt.Errorf("failed to decode API key: %w", err)
		}
		apiKey := string(apiKeyBytes)

		keyNameBytes, err := base64.StdEncoding.DecodeString(keyNameB64)
		if err != nil {
			return fmt.Errorf("failed to decode key name: %w", err)
		}
		keyName := string(keyNameBytes)

		// Delete the Connect API key via the Connect API
		if err := deleteConnectAPIKey(ctx, connectURL, apiKey, keyName); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to delete Connect API key: %v\n", err)
			// Continue with cleanup even if API key deletion fails
		} else {
			fmt.Fprintf(os.Stderr, "Deleted Connect API key: %s\n", keyName)
		}
	}

	// Delete the vip-test-credentials K8s Secret
	deleteCmd := exec.CommandContext(ctx, "kubectl", "delete", "secret", vipTestCredentialsSecret,
		"-n", namespace,
		"--ignore-not-found")
	deleteCmd.Env = env

	if err := deleteCmd.Run(); err != nil {
		return fmt.Errorf("failed to delete credentials secret: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Deleted credentials secret: %s\n", vipTestCredentialsSecret)
	return nil
}

// deleteConnectAPIKey deletes a Connect API key by name using the Connect API.
func deleteConnectAPIKey(ctx context.Context, connectURL, apiKey, keyName string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	// GET all API keys for the user
	listURL := fmt.Sprintf("%s/__api__/v1/user/api_keys", connectURL)
	req, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Key "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("list API keys failed with status %d: %s", resp.StatusCode, string(body))
	}

	var apiKeys []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiKeys); err != nil {
		return fmt.Errorf("failed to parse API keys response: %w", err)
	}

	// Find the key by name
	var keyID string
	for _, key := range apiKeys {
		if key.Name == keyName {
			keyID = key.ID
			break
		}
	}

	if keyID == "" {
		return fmt.Errorf("API key with name %q not found", keyName)
	}

	// DELETE the API key by ID
	deleteURL := fmt.Sprintf("%s/__api__/v1/user/api_keys/%s", connectURL, keyID)
	req, err = http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Key "+apiKey)

	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete API key failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
