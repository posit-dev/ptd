package steps

import (
	"context"

	"github.com/posit-dev/ptd/lib/types"
)

type PersistentRepriseStep struct {
	SrcTarget      types.Target
	DstTarget      types.Target
	Options        StepOptions
	persistentStep *PersistentStep
}

func (s *PersistentRepriseStep) Name() string {
	return "persistent_reprise"
}

func (s *PersistentRepriseStep) ProxyRequired() bool {
	return false
}

func (s *PersistentRepriseStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	// this is really just a replay of the persistent step at a later time.
	// it's pretty bogus, but it's what we have for now.
	s.persistentStep = &PersistentStep{
		SrcTarget: controlRoomTarget,
		DstTarget: t,
		Options:   options,
	}

	s.SrcTarget = s.persistentStep.SrcTarget
	s.DstTarget = s.persistentStep.DstTarget
	s.Options = s.persistentStep.Options

}

func (s *PersistentRepriseStep) Run(ctx context.Context) error {
	return s.persistentStep.Run(ctx)
}
