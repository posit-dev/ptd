package steps

import (
	"context"

	"github.com/posit-dev/ptd/lib/pulumi"
	"github.com/posit-dev/ptd/lib/types"
)

type PostgresConfigStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *PostgresConfigStep) Name() string {
	return "postgres_config"
}

func (s *PostgresConfigStep) ProxyRequired() bool {
	return true // connects to database
}

func (s *PostgresConfigStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *PostgresConfigStep) Run(ctx context.Context) error {
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

	// if the target isn't tailscale enabled, add ALL_PROXY to the env vars
	if !s.DstTarget.TailscaleEnabled() {
		envVars["ALL_PROXY"] = "socks5://localhost:1080" // TODO: make this configurable
	}

	stack, err := pulumi.NewPythonPulumiStack(
		ctx,
		string(s.DstTarget.CloudProvider()), // ptd-<cloud>-<control-room/workload>-<stackname>
		targetType,
		"postgres_config",
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
