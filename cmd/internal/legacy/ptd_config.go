package legacy

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/posit-dev/ptd/lib/aws"
	"github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/steps"
	"github.com/posit-dev/ptd/lib/types"
	"github.com/spf13/cobra"
)

const (
	CtrlDir = "__ctrl__"
	WorkDir = "__work__"
)

// findTargetDir returns the infrastructure subdirectory (CtrlDir or WorkDir)
// for a given target by checking which directory contains the target.
// Returns an error if the target exists in both directories or in neither.
func findTargetDir(infraRoot, target string) (string, error) {
	workPath := filepath.Join(infraRoot, WorkDir, target)
	ctrlPath := filepath.Join(infraRoot, CtrlDir, target)

	_, workErr := os.Stat(workPath)
	_, ctrlErr := os.Stat(ctrlPath)

	workExists := workErr == nil
	ctrlExists := ctrlErr == nil

	if workExists && ctrlExists {
		return "", fmt.Errorf("target %s exists in both %s and %s directories - unable to determine desired directory", target, WorkDir, CtrlDir)
	}

	if !workExists && !ctrlExists {
		return "", fmt.Errorf("target %s not found in either %s or %s directories", target, WorkDir, CtrlDir)
	}

	if workExists {
		return WorkDir, nil
	}
	return CtrlDir, nil
}

func ptdYamlFromTargetName(target string) (string, error) {
	infraRoot := helpers.GetTargetsConfigPath()
	infraSubDir, err := findTargetDir(infraRoot, target)
	if err != nil {
		return "", err
	}
	return filepath.Join(infraRoot, infraSubDir, target, "ptd.yaml"), nil
}

// WorkloadPathFromTargetName returns the workload directory path for a given target name
func WorkloadPathFromTargetName(target string) string {
	infraSubDir := WorkDir
	if strings.Contains(target, "main01") {
		infraSubDir = CtrlDir
	}
	infraRoot := helpers.GetTargetsConfigPath()
	return filepath.Join(infraRoot, infraSubDir, target)
}

// ValidTargetArgs returns a list of valid targets for the ensure command.
func ValidTargetArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		infraRoot := helpers.GetTargetsConfigPath()
		// collect all targets from both control room and workload directories
		var validTargets []string
		for _, dir := range []string{CtrlDir, WorkDir} {
			dirPath := filepath.Join(infraRoot, dir)
			targets, err := os.ReadDir(dirPath)
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}

			for _, target := range targets {
				if target.IsDir() && strings.HasPrefix(target.Name(), toComplete) {
					validTargets = append(validTargets, target.Name())
				}
			}
		}

		slices.Sort(validTargets)
		validTargets = slices.Compact(validTargets)

		return validTargets, cobra.ShellCompDirectiveNoFileComp
	} else if len(args) == 1 && cmd.Name() == "workon" {
		target := args[0]
		t, err := TargetFromName(target)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		var step_choices []string
		if t.ControlRoom() {
			step_choices = steps.Names(steps.ControlRoomSteps)
		} else {
			step_choices = steps.Names(steps.WorkloadSteps)
		}

		return step_choices, cobra.ShellCompDirectiveNoFileComp
	}
	return []string{}, cobra.ShellCompDirectiveNoFileComp
}

func TargetFromName(target string) (t types.Target, err error) {
	conf, err := loadConfig(target)
	if err != nil {
		return
	}

	switch conf.(type) {
	case types.AzureWorkloadConfig:
		return azure.NewTarget(
			target,
			conf.(types.AzureWorkloadConfig).SubscriptionID,
			conf.(types.AzureWorkloadConfig).TenantID,
			conf.(types.AzureWorkloadConfig).Region,
			conf.(types.AzureWorkloadConfig).Sites,
			conf.(types.AzureWorkloadConfig).AdminGroupID,
			conf.(types.AzureWorkloadConfig).Network.VnetRsgName,
			conf.(types.AzureWorkloadConfig).Clusters), nil
	case types.AWSWorkloadConfig:
		return aws.NewTarget(
			target,
			conf.(types.AWSWorkloadConfig).AccountID,
			conf.(types.AWSWorkloadConfig).Profile,
			conf.(types.AWSWorkloadConfig).CustomRole,
			conf.(types.AWSWorkloadConfig).Region,
			false, // isControlRoom
			conf.(types.AWSWorkloadConfig).TailscaleEnabled,
			conf.(types.AWSWorkloadConfig).CreateAdminPolicyAsResource,
			conf.(types.AWSWorkloadConfig).Sites,
			conf.(types.AWSWorkloadConfig).Clusters), nil
	case types.AWSControlRoomConfig:
		return aws.NewTarget(
			target,
			conf.(types.AWSControlRoomConfig).AccountID,
			"",  // profile is not relevant for control room targets
			nil, // customRole is not relevant for control room targets
			conf.(types.AWSControlRoomConfig).Region,
			true, // isControlRoom
			conf.(types.AWSControlRoomConfig).TailscaleEnabled,
			false, // createAdminPolicyAsResource is not relevant for control room targets
			nil,
			nil), nil
	}
	return
}

func ControlRoomTargetFromName(target string) (t types.Target, err error) {
	conf, err := loadConfig(target)
	if err != nil {
		return
	}

	switch conf.(type) {
	case types.AWSControlRoomConfig:
		return nil, fmt.Errorf("cannot create control room target from control room config")
	case types.AzureWorkloadConfig:
		return aws.NewTarget(
			conf.(types.AzureWorkloadConfig).ControlRoomClusterName,
			conf.(types.AzureWorkloadConfig).ControlRoomAccountID,
			"",  // profile is not relevant for control room targets
			nil, // customRole is not relevant for control room targets
			conf.(types.AzureWorkloadConfig).ControlRoomRegion,
			true,  // isControlRoom
			false, // tailscaleEnabled isn't relevant for control room.
			false, // createAdminPolicyAsResource is not relevant for control room targets
			nil,
			nil), nil
	case types.AWSWorkloadConfig:
		return aws.NewTarget(
			conf.(types.AWSWorkloadConfig).ControlRoomClusterName,
			conf.(types.AWSWorkloadConfig).ControlRoomAccountID,
			"",  // profile is not used for control room targets
			nil, // customRole is not used for control room targets
			conf.(types.AWSWorkloadConfig).ControlRoomRegion,
			true, // isControlRoom
			conf.(types.AWSWorkloadConfig).TailscaleEnabled,
			false, // createAdminPolicyAsResource is not relevant for control room targets
			nil,
			nil), nil
	}
	return
}

func loadConfig(target string) (interface{}, error) {
	pathToPtdYaml, err := ptdYamlFromTargetName(target)
	if err != nil {
		return nil, fmt.Errorf("could not determine target directory: %s", err.Error())
	}
	conf, err := helpers.LoadPtdYaml(pathToPtdYaml)
	if err != nil {
		return nil, fmt.Errorf("could not load relevant ptd.yaml file: %s", err.Error())
	}

	return conf, nil
}
