package eject

import (
	"context"
	"fmt"
	"testing"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/posit-dev/ptd/lib/types/typestest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCheckControlRoomConfigured_AWSWithFields(t *testing.T) {
	result := &PreflightResult{}
	config := types.AWSWorkloadConfig{
		ControlRoomAccountID: "123456789012",
		ControlRoomDomain:    "ctrl.example.com",
	}
	checkControlRoomConfigured(result, config)

	require.Len(t, result.Checks, 1)
	assert.Equal(t, CheckPass, result.Checks[0].Status)
	assert.Equal(t, "control_room_configured", result.Checks[0].Name)
}

func TestCheckControlRoomConfigured_AWSEmpty(t *testing.T) {
	result := &PreflightResult{}
	checkControlRoomConfigured(result, types.AWSWorkloadConfig{})

	require.Len(t, result.Checks, 1)
	assert.Equal(t, CheckFail, result.Checks[0].Status)
	assert.Contains(t, result.Checks[0].Message, "already be ejected")
}

func TestCheckControlRoomConfigured_AzureWithFields(t *testing.T) {
	result := &PreflightResult{}
	config := types.AzureWorkloadConfig{
		ControlRoomAccountID: "123456789012",
		ControlRoomDomain:    "ctrl.example.com",
	}
	checkControlRoomConfigured(result, config)

	require.Len(t, result.Checks, 1)
	assert.Equal(t, CheckPass, result.Checks[0].Status)
}

func TestCheckControlRoomConfigured_AzureEmpty(t *testing.T) {
	result := &PreflightResult{}
	checkControlRoomConfigured(result, types.AzureWorkloadConfig{})

	require.Len(t, result.Checks, 1)
	assert.Equal(t, CheckFail, result.Checks[0].Status)
}

func TestCheckControlRoomConfigured_UnsupportedType(t *testing.T) {
	result := &PreflightResult{}
	checkControlRoomConfigured(result, "not a config")

	require.Len(t, result.Checks, 1)
	assert.Equal(t, CheckFail, result.Checks[0].Status)
	assert.Contains(t, result.Checks[0].Message, "Unsupported config type")
}

func TestCheckControlRoomConfigured_PartialFields(t *testing.T) {
	result := &PreflightResult{}
	config := types.AWSWorkloadConfig{
		ControlRoomRegion: "us-east-1",
	}
	checkControlRoomConfigured(result, config)

	require.Len(t, result.Checks, 1)
	assert.Equal(t, CheckPass, result.Checks[0].Status)
}

func TestCheckCredentials_Valid(t *testing.T) {
	result := &PreflightResult{}
	target := &typestest.MockTarget{}
	creds := typestest.DefaultCredentials()
	target.On("Credentials", mock.Anything).Return(creds, nil)

	checkCredentials(context.Background(), result, target)

	require.Len(t, result.Checks, 1)
	assert.Equal(t, CheckPass, result.Checks[0].Status)
	assert.Equal(t, "workload_credentials", result.Checks[0].Name)
}

func TestCheckCredentials_GetFails(t *testing.T) {
	result := &PreflightResult{}
	target := &typestest.MockTarget{}
	target.On("Credentials", mock.Anything).Return((*typestest.MockCredentials)(nil), fmt.Errorf("no credentials"))

	checkCredentials(context.Background(), result, target)

	require.Len(t, result.Checks, 1)
	assert.Equal(t, CheckFail, result.Checks[0].Status)
	assert.Contains(t, result.Checks[0].Message, "no credentials")
}

func TestCheckCredentials_RefreshFails(t *testing.T) {
	result := &PreflightResult{}
	target := &typestest.MockTarget{}
	creds := &typestest.MockCredentials{}
	creds.On("Refresh", mock.Anything).Return(fmt.Errorf("expired"))
	target.On("Credentials", mock.Anything).Return(creds, nil)

	checkCredentials(context.Background(), result, target)

	require.Len(t, result.Checks, 1)
	assert.Equal(t, CheckFail, result.Checks[0].Status)
	assert.Contains(t, result.Checks[0].Message, "expired")
}

func TestCheckControlRoomCredentials_Valid(t *testing.T) {
	result := &PreflightResult{}
	target := &typestest.MockTarget{}
	creds := typestest.DefaultCredentials()
	target.On("Credentials", mock.Anything).Return(creds, nil)

	checkControlRoomCredentials(context.Background(), result, target)

	require.Len(t, result.Checks, 1)
	assert.Equal(t, CheckPass, result.Checks[0].Status)
	assert.Equal(t, "control_room_reachable", result.Checks[0].Name)
}

func TestCheckControlRoomCredentials_Fails(t *testing.T) {
	result := &PreflightResult{}
	target := &typestest.MockTarget{}
	target.On("Credentials", mock.Anything).Return((*typestest.MockCredentials)(nil), fmt.Errorf("access denied"))

	checkControlRoomCredentials(context.Background(), result, target)

	require.Len(t, result.Checks, 1)
	assert.Equal(t, CheckFail, result.Checks[0].Status)
	assert.Contains(t, result.Checks[0].Message, "access denied")
}

func TestPreflightResult_ComputePassed_AllPass(t *testing.T) {
	result := &PreflightResult{}
	result.addCheck("a", CheckPass, "ok")
	result.addCheck("b", CheckPass, "ok")
	result.addCheck("c", CheckWarn, "meh")
	result.addCheck("d", CheckSkip, "skipped")
	result.computePassed()

	assert.True(t, result.Passed)
}

func TestPreflightResult_ComputePassed_HasFail(t *testing.T) {
	result := &PreflightResult{}
	result.addCheck("a", CheckPass, "ok")
	result.addCheck("b", CheckFail, "bad")
	result.computePassed()

	assert.False(t, result.Passed)
}

func TestPreflightResult_ComputePassed_Empty(t *testing.T) {
	result := &PreflightResult{}
	result.computePassed()

	assert.True(t, result.Passed)
}

func TestRunPreflightChecks_SkipsControlRoomWhenNil(t *testing.T) {
	target := &typestest.MockTarget{}
	creds := typestest.DefaultCredentials()
	target.On("Credentials", mock.Anything).Return(creds, nil)

	result, err := RunPreflightChecks(context.Background(), target, PreflightOptions{
		Config:            types.AWSWorkloadConfig{ControlRoomDomain: "ctrl.example.com"},
		ControlRoomTarget: nil,
	})

	require.NoError(t, err)

	var crCheck *CheckResult
	for _, c := range result.Checks {
		if c.Name == "control_room_reachable" {
			crCheck = &c
			break
		}
	}
	require.NotNil(t, crCheck)
	assert.Equal(t, CheckSkip, crCheck.Status)
}

func TestRunPreflightChecks_AllPass(t *testing.T) {
	target := &typestest.MockTarget{}
	creds := typestest.DefaultCredentials()
	target.On("Credentials", mock.Anything).Return(creds, nil)

	crTarget := &typestest.MockTarget{}
	crTarget.On("Credentials", mock.Anything).Return(creds, nil)

	result, err := RunPreflightChecks(context.Background(), target, PreflightOptions{
		Config: types.AWSWorkloadConfig{
			ControlRoomAccountID: "123456789012",
			ControlRoomDomain:    "ctrl.example.com",
		},
		ControlRoomTarget: crTarget,
	})

	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Len(t, result.Checks, 3)
}

func TestRunPreflightChecks_FailsOnEmptyConfig(t *testing.T) {
	target := &typestest.MockTarget{}
	creds := typestest.DefaultCredentials()
	target.On("Credentials", mock.Anything).Return(creds, nil)

	result, err := RunPreflightChecks(context.Background(), target, PreflightOptions{
		Config: types.AWSWorkloadConfig{},
	})

	require.NoError(t, err)
	assert.False(t, result.Passed)
}
