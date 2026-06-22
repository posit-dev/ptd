package eject

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/posit-dev/ptd/lib/types"
)

func RemoveWorkloadMimirPassword(ctx context.Context, controlRoomTarget types.Target, workloadName string) error {
	creds, err := controlRoomTarget.Credentials(ctx)
	if err != nil {
		return fmt.Errorf("getting control room credentials: %w", err)
	}

	secretName := fmt.Sprintf("%s.mimir-auth.posit.team", controlRoomTarget.Name())

	// Read the secret directly and inspect the error. We must not rely on
	// SecretExists here: provider implementations return false on ANY error
	// (including AccessDenied/throttling), which would let a transient or
	// permission failure masquerade as a successful no-op. Only a genuine
	// "not found" is a legitimate no-op success; any other error is returned
	// so the caller records the removal as failed.
	val, err := controlRoomTarget.SecretStore().GetSecretValue(ctx, creds, secretName)
	if err != nil {
		if errors.Is(err, types.ErrSecretNotFound) {
			slog.Info("Mimir auth secret does not exist, nothing to remove", "secret", secretName)
			return nil
		}
		return fmt.Errorf("getting mimir auth secret: %w", err)
	}

	var secret map[string]string
	if err := json.Unmarshal([]byte(val), &secret); err != nil {
		return fmt.Errorf("unmarshalling mimir auth secret: %w", err)
	}

	if _, exists := secret[workloadName]; !exists {
		slog.Info("Workload not found in mimir auth secret, nothing to remove", "workload", workloadName, "secret", secretName)
		return nil
	}

	delete(secret, workloadName)

	if len(secret) == 0 {
		slog.Info("Mimir auth secret is now empty after removal", "secret", secretName)
	}

	secretString, err := json.Marshal(secret)
	if err != nil {
		return fmt.Errorf("marshalling mimir auth secret: %w", err)
	}

	if err := controlRoomTarget.SecretStore().PutSecretValue(ctx, creds, secretName, string(secretString)); err != nil {
		return fmt.Errorf("writing mimir auth secret: %w", err)
	}

	slog.Info("Removed workload mimir password from control room", "workload", workloadName, "secret", secretName)
	return nil
}
