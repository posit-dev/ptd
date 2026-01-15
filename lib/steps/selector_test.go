package steps

import (
	"context"
	"testing"

	"github.com/rstudio/ptd/lib/types"
	"github.com/rstudio/ptd/lib/types/typestest"

	"github.com/stretchr/testify/assert"
)

func TestSelectorEmpty(t *testing.T) {
	s := Selector("test_step", map[types.CloudProvider]Step{})

	target, ctrlTarget, options := setupStepArgs(types.None)

	assert.Equal(t, "test_step", s.Name(), "Selector name should match")
	assert.Equal(t, false, s.ProxyRequired(), "ProxyRequired should be false when no steps are set")

	// No assertion because Set does not have a return value. Still useful to run and ensure no panic occurs.
	s.Set(target, ctrlTarget, options)

	assert.NoError(t, s.Run(context.Background()), "Run should not return an error when no steps are set")
}

func TestSelectorWithValidProviderStep(t *testing.T) {
	mockAws := &MockStep{}
	mockAzure := &MockStep{}
	ctx := context.Background()

	s := Selector("test_step", map[types.CloudProvider]Step{
		types.AWS:   mockAws,
		types.Azure: mockAzure,
	})

	target, ctrlTarget, options := setupStepArgs(types.AWS)

	assert.Equal(t, "test_step", s.Name(), "Selector name should not be delegated")

	mockAws.On("Set", target, ctrlTarget, options).Return()
	s.Set(target, ctrlTarget, options)
	mockAws.AssertCalled(t, "Set", target, ctrlTarget, options)
	mockAzure.AssertNotCalled(t, "Set")

	mockAws.On("Run", ctx).Return(nil)
	err := s.Run(ctx)
	assert.NoError(t, err, "Run should not return an error when a valid step is set for the selected cloud provider")
	mockAws.AssertCalled(t, "Run", ctx)

	mockAws.On("ProxyRequired").Return(true)
	assert.True(t, s.ProxyRequired(), "ProxyRequired should return true when the selected step requires a proxy")
	mockAws.AssertCalled(t, "ProxyRequired")
}

func TestSelectorWithInvalidProviderStep(t *testing.T) {
	mockAws := &MockStep{}
	ctx := context.Background()

	s := Selector("test_step", map[types.CloudProvider]Step{
		types.AWS: mockAws,
	})

	target, ctrlTarget, options := setupStepArgs(types.Azure)

	assert.Equal(t, "test_step", s.Name(), "Selector name should not be delegated")

	s.Set(target, ctrlTarget, options)
	mockAws.AssertNotCalled(t, "Set")

	err := s.Run(ctx)
	assert.NoError(t, err, "Run should not return an error when a valid step is set for the selected cloud provider")
	mockAws.AssertNotCalled(t, "Run")

	mockAws.On("ProxyRequired").Return(true)
	assert.False(t, s.ProxyRequired(), "ProxyRequired should return false by default when no step is set")
	mockAws.AssertNotCalled(t, "ProxyRequired")
}

func setupStepArgs(p types.CloudProvider) (types.Target, types.Target, StepOptions) {
	target := &typestest.MockTarget{}
	target.On("CloudProvider").Return(p)
	return target, &typestest.MockTarget{}, StepOptions{}
}
