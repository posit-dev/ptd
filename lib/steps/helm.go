package steps

import (
	"context"

	"github.com/posit-dev/ptd/lib/pulumi"
	"github.com/posit-dev/ptd/lib/types"
)

type HelmStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *HelmStep) Name() string {
	return "helm"
}

func (s *HelmStep) ProxyRequired() bool {
	return true
}

func (s *HelmStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *HelmStep) Run(ctx context.Context) error {
	targetType := "workload"
	if s.DstTarget.ControlRoom() {
		targetType = "control-room"
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

	stack, err := pulumi.NewPythonPulumiStack(
		ctx,
		string(s.DstTarget.CloudProvider()), // ptd-<cloud>-<control-room/workload>-<stackname>
		targetType,
		"helm",
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

	err = pulumiRefreshPreviewUpCancel(ctx, stack, s.Options)
	if err != nil {
		return err
	}

	return nil
}
