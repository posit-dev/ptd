package steps

import (
	"context"
	"fmt"

	"github.com/posit-dev/ptd/lib/types"
)

type EKSStep struct {
	SrcTarget types.Target
	DstTarget types.Target
	Options   StepOptions
}

func (s *EKSStep) Name() string {
	return "eks"
}

func (s *EKSStep) ProxyRequired() bool {
	return true
}

func (s *EKSStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.SrcTarget = controlRoomTarget
	s.DstTarget = t
	s.Options = options
}

func (s *EKSStep) Run(ctx context.Context) error {
	if s.DstTarget == nil {
		return fmt.Errorf("eks step requires a destination target")
	}

	// eks is AWS-only (Azure workloads use the aks step).
	switch s.DstTarget.CloudProvider() {
	case types.AWS:
		return s.runAWSInlineGo(ctx)
	default:
		return fmt.Errorf("unsupported cloud provider for eks: %s", s.DstTarget.CloudProvider())
	}
}
