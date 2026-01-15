package main

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/rstudio/ptd/lib/aws"
	"github.com/rstudio/ptd/lib/azure"

	"github.com/rstudio/ptd/cmd/internal/legacy"
	"github.com/rstudio/ptd/lib/steps"
	"github.com/rstudio/ptd/lib/types"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(ensureCmd)

	ensureCmd.PersistentFlags().BoolVarP(&DryRun, "dry-run", "n", false, "Dry run the command")
	ensureCmd.PersistentFlags().BoolVarP(&Preview, "preview", "p", true, "Preview the stack changes before (optional) up")
	ensureCmd.PersistentFlags().BoolVarP(&Cancel, "cancel", "c", false, "Clear locks from the stack")
	ensureCmd.PersistentFlags().BoolVarP(&Refresh, "refresh", "r", false, "Refresh the stack state before (optional) up")
	ensureCmd.PersistentFlags().BoolVarP(&AutoApply, "auto-apply", "a", false, "Skip manual approval and automatically apply changes")
	ensureCmd.PersistentFlags().BoolVarP(&Destroy, "destroy", "", false, "Destroy the Pulumi stack")
	ensureCmd.PersistentFlags().StringVar(&StartAtStep, "start-at-step", "", "Start at a specific step")
	ensureCmd.PersistentFlags().StringSliceVarP(&OnlySteps, "only-steps", "", nil, "Only run specific steps")
	ensureCmd.PersistentFlags().StringSliceVarP(&ExcludedResources, "exclude-resources", "", nil, "Exclude specific resources from the ensure process")
	ensureCmd.PersistentFlags().StringSliceVarP(&TargetResources, "target-resources", "", nil, "Target specific resources for the ensure process")
	ensureCmd.PersistentFlags().BoolVarP(&ListSteps, "list-steps", "l", false, "List all steps for the target (including custom steps) and exit")

	step_choices := steps.Names(steps.WorkloadSteps)
	step_choices = append(step_choices, steps.Names(steps.ControlRoomSteps)...)
	slices.Sort(step_choices)
	step_choices = slices.Compact(step_choices)

	// Register completion for the --start-at-step flag
	if err := ensureCmd.RegisterFlagCompletionFunc("start-at-step", cobra.FixedCompletions(
		step_choices, cobra.ShellCompDirectiveNoFileComp)); err != nil {
		slog.Error("Failed to register flag completion for start-at-step", "error", err)
	}

	// Register completion for the --only-steps flag
	if err := ensureCmd.RegisterFlagCompletionFunc("only-steps", cobra.FixedCompletions(
		step_choices, cobra.ShellCompDirectiveNoFileComp)); err != nil {
		slog.Error("Failed to register flag completion for only-steps", "error", err)
	}
}

var ensureCmd = &cobra.Command{
	Use:   "ensure <target>",
	Short: "Ensure a target is converged",
	Long:  `Ensure a target is converged.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster := args[0]
		runEnsure(cmd.Context(), cluster)
	},
	ValidArgsFunction: legacy.ValidTargetArgs,
}

var DryRun bool
var Preview bool
var Cancel bool
var Refresh bool
var AutoApply bool
var Destroy bool
var StartAtStep string
var OnlySteps []string
var ExcludedResources []string
var TargetResources []string
var ListSteps bool

func runEnsure(ctx context.Context, target string) {
	// find the relevant ptd.yaml file, load it.
	t, err := legacy.TargetFromName(target)
	if err != nil {
		slog.Error("Could not load relevant ptd.yaml file", "error", err)
		return
	}

	// Load steps with custom steps if available
	workloadPath := legacy.WorkloadPathFromTargetName(target)
	allSteps, err := steps.LoadStepsForWorkload(workloadPath, t.ControlRoom())
	if err != nil {
		slog.Error("Failed to load steps", "error", err)
		return
	}

	if ListSteps {
		listSteps(allSteps)
		return
	}

	// Validate step names against loaded steps
	stepNames := steps.Names(allSteps)
	for _, stepName := range OnlySteps {
		if !slices.Contains(stepNames, stepName) {
			slog.Error("Invalid step found in --only-steps", "step", stepName)
			return
		}
	}

	if StartAtStep != "" && !slices.Contains(stepNames, StartAtStep) {
		slog.Error("Invalid step found in --start-at-step", "step", StartAtStep)
		return
	}

	// handle --only-steps
	stepsToRun := make([]steps.Step, len(allSteps))
	stepsToRun, err = steps.OnlySteps(allSteps, OnlySteps)
	if err != nil {
		slog.Error("Error when filtering steps", "error", err)
		return
	}

	// Need to reverse the steps if we're destroying
	if Destroy {
		slices.Reverse(stepsToRun)
	}

	// handle --start-at-step
	stepsToRun, err = steps.StartAtStep(stepsToRun, StartAtStep)
	if err != nil {
		slog.Error("Error when filtering steps", "error", err)
		return
	}

	// if we're working with a workload, we'll also need a control room target
	var controlRoomTarget types.Target
	if !t.ControlRoom() {
		// find the relevant ptd.yaml file, load it.
		controlRoomTarget, err = legacy.ControlRoomTargetFromName(target)
		if err != nil {
			slog.Error("Could not load relevant ptd.yaml file", "error", err)
			return
		}
	}

	// set options on each step before checking if proxy is required
	for _, step := range stepsToRun {
		step.Set(t, controlRoomTarget, steps.StepOptions{
			DryRun:            DryRun,
			Refresh:           Refresh,
			Cancel:            Cancel,
			Preview:           Preview,
			AutoApply:         AutoApply,
			Destroy:           Destroy,
			ExcludedResources: ExcludedResources,
			TargetResources:   TargetResources,
		})
	}

	// if any of the steps require a proxy, start the proxy session, unless tailscale is enabled
	if steps.ProxyRequiredSteps(stepsToRun) && !t.TailscaleEnabled() {
		if t.CloudProvider() == types.AWS {
			ps := aws.NewProxySession(t.(aws.Target), getAwsCliPath(), "1080", proxyFile)
			err = ps.Start(ctx)
			if err != nil {
				slog.Error("Error starting AWS proxy session", "error", err)
				return
			}
			defer ps.Stop()
		} else {
			ps := azure.NewProxySession(t.(azure.Target), getAzureCliPath(), "1080", proxyFile)
			err = ps.Start(ctx)
			if err != nil {
				slog.Error("Error starting Azure proxy session", "error", err)
				return
			}
			defer ps.Stop()
		}
	}

	for _, step := range stepsToRun {
		slog.Info("Running step", "step", step.Name())
		err := step.Run(ctx)
		if err != nil {
			slog.Error("Error running step", "step", step.Name(), "error", err)
			return
		}
	}
}

// listSteps displays all available steps for the target, marking custom steps
func listSteps(allSteps []steps.Step) {
	slog.Info("Available steps:")

	standardCount := 0
	customCount := 0

	for i, step := range allSteps {
		if steps.IsCustomStep(step) {
			if customCount == 0 {
				slog.Info("Custom Steps:")
			}
			customCount++
			slog.Info(fmt.Sprintf("  %d. %s [CUSTOM]", i+1, step.Name()))
			if cs, ok := step.(*steps.CustomStep); ok {
				if cs.Description() != "" {
					slog.Info(fmt.Sprintf("      %s", cs.Description()))
				}
			}
		} else {
			if standardCount == 0 {
				slog.Info("Standard Steps:")
			}
			standardCount++
			slog.Info(fmt.Sprintf("  %d. %s", i+1, step.Name()))
		}
	}

	slog.Info(fmt.Sprintf("Total: %d steps (%d standard, %d custom)", len(allSteps), standardCount, customCount))
}
