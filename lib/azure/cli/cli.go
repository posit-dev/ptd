// Package cli provides a simple wrapper around the Azure CLI for managing Azure resources.
// This is helpful because pulumi and our current azure efforts depend on the Azure CLI
package cli

import (
	"context"
	"os"
	"os/exec"
	"path"
)

type Az struct {
	cliPath  string
	mockMode bool
}

var (
	azInstance *Az
)

// GetAzInstance returns the singleton instance of Az.
// Always uses internal logic to determine cliPath.
func GetAzInstance() *Az {
	if azInstance == nil {
		azInstance = &Az{
			cliPath:  getCliPath(),
			mockMode: false,
		}
	}
	return azInstance
}

// SetMockMode enables mock mode for testing, which makes all CLI operations succeed
func SetMockMode(enabled bool) {
	if azInstance == nil {
		GetAzInstance()
	}
	azInstance.mockMode = enabled
}

// SetSubscription now takes context as a parameter
func (a *Az) SetSubscription(ctx context.Context, subscriptionID string) error {
	if a.mockMode {
		return nil
	}
	cmd := exec.CommandContext(ctx, a.cliPath, "account", "set", "--subscription", subscriptionID)
	return cmd.Run()
}

func (a *Az) GetAccessToken(ctx context.Context) error {
	if a.mockMode {
		return nil
	}
	cmd := exec.CommandContext(ctx, a.cliPath, "account", "get-access-token")
	return cmd.Run()
}

func getCliPath() (azureCliPath string) {
	azureCliPath = "az"
	top, ok := os.LookupEnv("TOP")
	if ok {
		azureCliPath = path.Join(top, ".local/bin/az")
	}
	return
}
