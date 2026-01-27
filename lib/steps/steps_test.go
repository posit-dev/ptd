package steps

import (
	"context"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	psdk "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/mock"

	"github.com/posit-dev/ptd/lib/testdata"
	"github.com/posit-dev/ptd/lib/types"
)

type stepTestHandler struct {
	stack  auto.Stack
	target types.Target
	deploy func(*psdk.Context, types.Target) error
}

func (h *stepTestHandler) createStack(ctx context.Context, name string, target types.Target, deploy func(*psdk.Context, types.Target) error, envVars map[string]string) (auto.Stack, error) {
	h.target = target
	h.deploy = deploy
	return h.stack, nil
}

func (h *stepTestHandler) runPulumi(ctx context.Context, stack auto.Stack, options StepOptions) error {
	if h.deploy != nil {
		return h.deploy(nil, h.target)
	}
	return nil
}

func setupStep(t *testing.T) (func(), error) {
	defaultRunPulumi := runPulumi
	defaultCreateStack := createStack

	sth := &stepTestHandler{}
	runPulumi = sth.runPulumi
	createStack = sth.createStack

	teardown, err := testdata.Setup(t)
	if err != nil {
		return func() {}, err
	}

	return func() {
		runPulumi = defaultRunPulumi
		createStack = defaultCreateStack
		teardown()
	}, nil
}

type MockStep struct {
	mock.Mock
}

func (m *MockStep) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	m.Called(t, controlRoomTarget, options)
}

func (m *MockStep) Run(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockStep) ProxyRequired() bool {
	args := m.Called()
	return args.Bool(0)
}
