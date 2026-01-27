package steps

import (
	"context"
	"fmt"

	"github.com/posit-dev/ptd/lib/pulumi"
	"github.com/posit-dev/ptd/lib/types"
)

type ClusterStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *ClusterStep) Name() string {
	return "cluster"
}

func (s *ClusterStep) ProxyRequired() bool {
	return true
}

func (s *ClusterStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *ClusterStep) Run(ctx context.Context) error {
	if !s.DstTarget.ControlRoom() {
		return fmt.Errorf("cluster step can only be run on control room targets")
	}

	targetType := "control-room"

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
		"cluster",
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

	return pulumiRefreshPreviewUpCancel(ctx, stack, s.Options)
}
