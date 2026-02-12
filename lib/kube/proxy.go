package kube

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"

	awslib "github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/types"
	"tailscale.com/client/local"
)

// StartProxy starts a SOCKS proxy session if needed for the target.
// For non-tailscale targets, it starts the appropriate proxy.
// For tailscale targets, it verifies connectivity and warns if not connected.
// Returns a stop function that should be deferred, and any error.
func StartProxy(ctx context.Context, t types.Target, proxyFile string) (stopFunc func(), err error) {
	if t.TailscaleEnabled() {
		// Check tailscale connectivity
		client := local.Client{}
		status, _ := client.Status(ctx)
		isTailscaleConnected := status != nil && status.BackendState == "Running"

		if !isTailscaleConnected {
			slog.Warn("Tailscale is not connected, connection may fail", "target_name", t.Name())
		}

		// Return a no-op stop function
		return func() {}, nil
	}

	// For non-tailscale targets, start the appropriate proxy
	switch t.CloudProvider() {
	case types.AWS:
		// Type-assert to aws.Target
		awsTarget, ok := t.(awslib.Target)
		if !ok {
			return nil, fmt.Errorf("failed to type-assert to aws.Target")
		}

		// Create and start AWS proxy session
		ps := awslib.NewProxySession(awsTarget, GetCliPath(types.AWS), "1080", proxyFile)
		if err := ps.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start AWS proxy session: %w", err)
		}

		// Return the stop function
		return func() {
			if stopErr := ps.Stop(); stopErr != nil {
				slog.Error("Failed to stop AWS proxy session", "error", stopErr)
			}
		}, nil

	case types.Azure:
		// Type-assert to azure.Target
		azureTarget, ok := t.(azure.Target)
		if !ok {
			return nil, fmt.Errorf("failed to type-assert to azure.Target")
		}

		// Create and start Azure proxy session
		ps := azure.NewProxySession(azureTarget, GetCliPath(types.Azure), "1080", proxyFile)
		if err := ps.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start Azure proxy session: %w", err)
		}

		// Return the stop function
		return func() {
			if stopErr := ps.Stop(); stopErr != nil {
				slog.Error("Failed to stop Azure proxy session", "error", stopErr)
			}
		}, nil

	default:
		return nil, fmt.Errorf("proxy not implemented for cloud provider: %s", t.CloudProvider())
	}
}

// GetCliPath returns the path to the cloud provider CLI binary.
func GetCliPath(provider types.CloudProvider) string {
	top, ok := os.LookupEnv("TOP")

	switch provider {
	case types.AWS:
		if ok {
			return path.Join(top, ".local/bin/aws")
		}
		return "aws"
	case types.Azure:
		if ok {
			return path.Join(top, ".local/bin/az")
		}
		return "az"
	default:
		// Return empty string for unsupported providers
		return ""
	}
}