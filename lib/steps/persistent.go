package steps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"

	"github.com/posit-dev/ptd/lib/pulumi"
	"github.com/posit-dev/ptd/lib/secrets"
	"github.com/posit-dev/ptd/lib/types"
)

type PersistentStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *PersistentStep) Name() string {
	return "persistent"
}

func (s *PersistentStep) ProxyRequired() bool {
	return false
}

func (s *PersistentStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *PersistentStep) Run(ctx context.Context) error {
	// this step is a little special because the workload persistent step
	// has a secret output which also needs to be stored in the control room.
	if s.DstTarget == nil {
		return errors.New("persistent step requires a destination target")
	}

	// get the credentials for the target
	creds, err := s.DstTarget.Credentials(ctx)
	if err != nil {
		return err
	}
	envVars, err := prepareEnvVarsForPulumi(ctx, s.DstTarget, creds)
	if err != nil {
		return err
	}

	slog.Debug("Creating Pulumi stack for persistent step",
		"target", s.DstTarget.Name(),
		"cloud", s.DstTarget.CloudProvider(),
		"backend_url", s.DstTarget.PulumiBackendUrl(),
		"env_var_count", len(envVars))

	// Dispatch to the inline-Go deploy for the target's cloud / type. Each
	// build*Stack method pre-fetches external data, constructs the inline Pulumi
	// stack, and then drives it via runPersistentStack (below), which preserves
	// the bespoke post-apply side effects this step needs (mimir password sync +
	// EnsureWorkloadSecret) — the shared runPulumi helper discards stack outputs.
	switch s.DstTarget.CloudProvider() {
	case types.AWS:
		if s.DstTarget.ControlRoom() {
			return s.runAWSControlRoomInlineGo(ctx, creds, envVars)
		}
		return s.runAWSInlineGo(ctx, creds, envVars)
	case types.Azure:
		if s.DstTarget.ControlRoom() {
			return fmt.Errorf("persistent: azure control room is not supported")
		}
		return s.runAzureInlineGo(ctx, creds, envVars)
	default:
		return fmt.Errorf("unsupported cloud provider for persistent: %s", s.DstTarget.CloudProvider())
	}
}

// runPersistentStack drives an already-constructed persistent stack through the
// refresh/cancel/destroy/up lifecycle and performs the persistent step's two
// post-apply side effects for AWS workloads: syncing the mimir password into the
// control room and ensuring the workload secret. It deliberately does NOT use
// the shared runPulumi helper because that discards the up result's outputs,
// which these side effects require.
func (s *PersistentStep) runPersistentStack(ctx context.Context, stack auto.Stack, creds types.Credentials) error {
	// output pulumi whoami details every time
	err := pulumi.Whoami(ctx, stack)
	if err != nil {
		return err
	}

	if s.Options.Cancel {
		return pulumi.CancelStack(ctx, stack)
	}

	if s.Options.Refresh && s.Options.DryRun {
		return fmt.Errorf("refresh and dryRun are mutually exclusive")
	}

	// perform refresh separately from preview/up
	if s.Options.Refresh {
		_, err := pulumi.RefreshStack(ctx, stack)
		if err != nil {
			return err
		}
	}

	// if we're working on a workload, grab the current mimir password
	currentMimirPassword := ""
	if !s.DstTarget.ControlRoom() && s.DstTarget.CloudProvider() == types.AWS {
		oldOutputs, err := stack.Outputs(ctx)
		if err != nil {
			return err
		}
		if currentMimirPasswordOutput, ok := oldOutputs["mimir_password"]; ok {
			currentMimirPassword = currentMimirPasswordOutput.Value.(string)
		}
	}

	if s.Options.Destroy {
		_, err = pulumi.DestroyStack(ctx, stack, s.Options.Preview, s.Options.DryRun, s.Options.ExcludedResources, s.Options.TargetResources)
		if err != nil {
			return err
		}
		return nil
	}
	result, err := pulumi.UpStack(ctx, stack, s.Options.Preview, s.Options.DryRun, s.Options.AutoApply, s.Options.ExcludedResources, s.Options.TargetResources)
	if err != nil {
		return err
	}

	// if we're dry running, ignore the remainder.
	if s.Options.DryRun {
		return nil
	}

	// the outputs for a result will be empty if stack.Up was never executed.
	// this is the case for a when a preview produced no diff.
	// in this case, ignore the remainder
	if len(result.Outputs) == 0 {
		return nil
	}

	// if we're working on a workload, this password may have changed, update if so.
	if !s.DstTarget.ControlRoom() && s.SrcTarget != nil {
		newMimirPassword, ok := result.Outputs["mimir_password"].Value.(string)
		if !ok {
			return fmt.Errorf("mimir_password not found in outputs")
		}
		if newMimirPassword != currentMimirPassword {
			slog.Info("Updating control room mimir password", "target", s.DstTarget.Name())
			return updateControlRoomMimirPassword(ctx, s.SrcTarget, s.DstTarget.Name(), newMimirPassword)
		}
	}

	// if we're working on a workload, we also need to ensure the secret is updated
	// this only works for AWS because *annoyed*
	// TODO: this should be a pulumi resource.
	if !s.DstTarget.ControlRoom() && s.DstTarget.CloudProvider() == types.AWS {
		slog.Info("Ensuring workload secret for target", "target", s.DstTarget.Name())
		secret := secrets.AWSWorkloadSecret{
			ChronicleBucket:      result.Outputs["chronicle_bucket"].Value.(string),
			FsDnsName:            result.Outputs["fs_dns_name"].Value.(string),
			FsRootVolumeID:       result.Outputs["fs_root_volume_id"].Value.(string),
			MainDatabaseID:       result.Outputs["db"].Value.(string),
			MainDatabaseURL:      result.Outputs["db_url"].Value.(string),
			PackageManagerBucket: result.Outputs["packagemanager_bucket"].Value.(string),
			MimirPassword:        result.Outputs["mimir_password"].Value.(string),
		}

		err = s.DstTarget.SecretStore().EnsureWorkloadSecret(ctx, creds, s.DstTarget.Name(), secret)
		if err != nil {
			return err
		}
	}

	return nil
}

func updateControlRoomMimirPassword(ctx context.Context, controlRoomTarget types.Target, workloadName string, newPassword string) error {
	creds, err := controlRoomTarget.Credentials(ctx)
	if err != nil {
		return err
	}

	secretName := fmt.Sprintf("%s.mimir-auth.posit.team", controlRoomTarget.Name())
	val, err := controlRoomTarget.SecretStore().GetSecretValue(ctx, creds, secretName)
	if err != nil {
		return err
	}

	var secret map[string]string
	err = json.Unmarshal([]byte(val), &secret)
	if err != nil {
		return err
	}

	secret[workloadName] = newPassword
	secretString, err := json.Marshal(secret)
	if err != nil {
		return err
	}

	return controlRoomTarget.SecretStore().PutSecretValue(ctx, creds, secretName, string(secretString))
}
