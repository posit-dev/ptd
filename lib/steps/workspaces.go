package steps

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/posit-dev/ptd/lib/helpers"
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

	// The AWS WorkSpaces environment is opt-out per control room via the
	// `workspaces_enabled` config toggle (default on). When a control room sets
	// `workspaces_enabled: false`, the whole step is a no-op. This gate is loaded and
	// evaluated before any credential fetch or Pulumi stack creation so it applies to
	// BOTH the apply and destroy paths: a `--destroy` on a control room that has the
	// toggle off is also a no-op (once the stack has been destroyed it stays gone).
	// Config is loaded the same way runAWSInlineGo loads it (helpers.ConfigForTarget +
	// type-assert to AWSControlRoomConfig), which is the only control-room config kind.
	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("workspaces: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AWSControlRoomConfig)
	if !ok {
		return fmt.Errorf("workspaces: expected AWSControlRoomConfig, got %T", rawConfig)
	}
	if !cfg.WorkspacesIsEnabled() {
		slog.Info("skipping workspaces step: workspaces_enabled is false for this control room",
			"target", s.DstTarget.Name())
		return nil
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
