package steps

import (
	"context"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	psdk "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/posit-dev/ptd/lib/testdata"
	"github.com/posit-dev/ptd/lib/types"
)

func TestResolveStackStep(t *testing.T) {
	tests := []struct {
		name          string
		step          string
		controlRoom   bool
		cloudProvider types.CloudProvider
		wantStep      string
		wantOk        bool
	}{
		{
			name:          "kubernetes resolves to eks on AWS",
			step:          "kubernetes",
			cloudProvider: types.AWS,
			wantStep:      "eks",
			wantOk:        true,
		},
		{
			name:          "kubernetes resolves to aks on Azure",
			step:          "kubernetes",
			cloudProvider: types.Azure,
			wantStep:      "aks",
			wantOk:        true,
		},
		{
			name:          "eks resolves to itself on AWS",
			step:          "eks",
			cloudProvider: types.AWS,
			wantStep:      "eks",
			wantOk:        true,
		},
		{
			name:          "aks resolves to itself on Azure",
			step:          "aks",
			cloudProvider: types.Azure,
			wantStep:      "aks",
			wantOk:        true,
		},
		{
			name:          "eks does not resolve on Azure",
			step:          "eks",
			cloudProvider: types.Azure,
			wantOk:        false,
		},
		{
			name:          "aks does not resolve on AWS",
			step:          "aks",
			cloudProvider: types.AWS,
			wantOk:        false,
		},
		{
			name:          "non-selector step resolves to itself",
			step:          "bootstrap",
			cloudProvider: types.AWS,
			wantStep:      "bootstrap",
			wantOk:        true,
		},
		{
			name:          "unknown step does not resolve",
			step:          "bogus",
			cloudProvider: types.AWS,
			wantOk:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveStackStep(tt.step, tt.controlRoom, tt.cloudProvider)
			assert.Equal(t, tt.wantOk, ok)
			if tt.wantOk {
				assert.Equal(t, tt.wantStep, got)
			}
		})
	}
}

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

// boolPtr returns a pointer to b, for constructing pointer-typed config fields in tests.
func boolPtr(b bool) *bool { return &b }
