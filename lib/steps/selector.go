package steps

import (
	"context"
	"log/slog"

	"github.com/posit-dev/ptd/lib/types"
)

type selector struct {
	name     string
	steps    map[types.CloudProvider]Step
	selected types.CloudProvider
}

// Selector is a step that delegates to a specific step based on the cloud
// provider of the target.
func Selector(name string, steps map[types.CloudProvider]Step) *selector {
	return &selector{
		name:     name,
		steps:    steps,
		selected: types.None,
	}
}

func (s *selector) Name() string {
	return s.name
}

func (s *selector) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.selected = t.CloudProvider()

	if step, ok := s.steps[s.selected]; ok {
		step.Set(t, controlRoomTarget, options)
	}
}

func (s *selector) Run(ctx context.Context) error {
	step, ok := s.steps[s.selected]
	if !ok {
		slog.Warn("Skipping: No step found for selected cloud provider", "step", s.name, "cloud_provider", s.selected)
		return nil
	}

	return step.Run(ctx)
}

func (s *selector) ProxyRequired() bool {
	if step, ok := s.steps[s.selected]; ok {
		return step.ProxyRequired()
	}

	return false
}
