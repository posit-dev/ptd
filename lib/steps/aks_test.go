package steps

import (
	"context"
	"testing"

	"github.com/posit-dev/ptd/lib/types/typestest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAKSStepInvalidTarget(t *testing.T) {
	teardown, err := setupStep(t)
	require.NoError(t, err, "Failed to setup step test environment")
	defer teardown()

	step := &AKSStep{}
	tgt := typestest.DefaultAzureTarget()

	assert.Equal(t, step.Name(), "aks", "Expected step name to be 'aks'")
	assert.Equal(t, step.ProxyRequired(), false, "Expected ProxyRequired to return false")

	step.Set(tgt, nil, StepOptions{})

	err = step.Run(context.Background())
	assert.ErrorContains(t, err, "expected Azure target but got different type")
}
