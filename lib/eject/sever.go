package eject

import (
	"context"
	"encoding/json"
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

	if !controlRoomTarget.SecretStore().SecretExists(ctx, creds, secretName) {
		slog.Info("Mimir auth secret does not exist, nothing to remove", "secret", secretName)
		return nil
	}

	val, err := controlRoomTarget.SecretStore().GetSecretValue(ctx, creds, secretName)
	if err != nil {
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
