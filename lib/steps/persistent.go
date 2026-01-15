package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/rstudio/ptd/lib/pulumi"
	"github.com/rstudio/ptd/lib/secrets"
	"github.com/rstudio/ptd/lib/types"
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

	targetType := "workload"
	if s.DstTarget.ControlRoom() {
		targetType = "control-room"
	}

	// get the credentials for the target
	creds, err := s.DstTarget.Credentials(ctx)
	if err != nil {
		return err
	}
	envVars := creds.EnvVars()

	stack, err := pulumi.NewPythonPulumiStack(
		ctx,
		string(s.DstTarget.CloudProvider()), // ptd-<cloud>-<control-room/workload>-<stackname>
		targetType,
		"persistent",
		s.DstTarget.Name(),
		s.DstTarget.Region(),
		s.DstTarget.PulumiBackendUrl(),
		s.DstTarget.PulumiSecretsProviderKey(),
		envVars,
		true,
	)
	if err != nil {
		return err
	}

	// output pulumi whoami details every time
	err = pulumi.Whoami(ctx, stack)
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
	newMimirPassword, ok := result.Outputs["mimir_password"].Value.(string)
	if !ok {
		return fmt.Errorf("mimir_password not found in outputs")
	}
	if newMimirPassword != currentMimirPassword {
		slog.Info("Updating control room mimir password", "target", s.DstTarget.Name())
		return updateControlRoomMimirPassword(ctx, s.SrcTarget, s.DstTarget.Name(), newMimirPassword)
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
