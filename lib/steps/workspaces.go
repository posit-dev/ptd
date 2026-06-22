package steps

import (
	"context"
	"fmt"

	"github.com/posit-dev/ptd/lib/types"
)

// WorkspacesStep provisions the AWS Workspaces environment for a control room.
// This is an AWS-only step; Azure control rooms do not have a workspaces equivalent.
type WorkspacesStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *WorkspacesStep) Name() string {
	return "workspaces"
}

func (s *WorkspacesStep) ProxyRequired() bool {
	return false
}

func (s *WorkspacesStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *WorkspacesStep) Run(ctx context.Context) error {
	if s.DstTarget == nil {
		return fmt.Errorf("workspaces step requires a destination target")
	}

	if !s.DstTarget.ControlRoom() {
		return fmt.Errorf("workspaces step can only be run on control room targets")
	}

	creds, err := s.DstTarget.Credentials(ctx)
	if err != nil {
		return err
	}
	envVars, err := prepareEnvVarsForPulumi(ctx, s.DstTarget, creds)
	if err != nil {
		return err
	}

	switch s.DstTarget.CloudProvider() {
	case types.AWS:
		return s.runAWSInlineGo(ctx, creds, envVars)
	default:
		return fmt.Errorf("unsupported cloud provider for workspaces: %s", s.DstTarget.CloudProvider())
	}
}
