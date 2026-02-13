package steps

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/posit-dev/ptd/lib/pulumi"
	"github.com/posit-dev/ptd/lib/types"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
)

// These aren't the prettiest, but they allow us to easily mock the Pulumi
// operations in tests.
var createStack = pulumi.Stack
var runPulumi = pulumiRefreshPreviewUpCancel

var ControlRoomSteps = []Step{
	&WorkspacesStep{},
	&PersistentStep{},
	&PostgresConfigStep{},
	&ClusterStep{},
}

var WorkloadSteps = []Step{
	&BootstrapStep{},
	&PersistentStep{},
	&PostgresConfigStep{},
	Selector("kubernetes", map[types.CloudProvider]Step{
		types.AWS:   &EKSStep{},
		types.Azure: &AKSStep{},
	}),
	&ClustersStep{},
	&HelmStep{},
	&SitesStep{},
	&PersistentRepriseStep{},
}

func Names(steps []Step) (names []string) {
	for _, step := range steps {
		names = append(names, step.Name())
	}
	return
}

func ValidStep(step string, controlRoom bool) bool {
	if controlRoom {
		for _, s := range ControlRoomSteps {
			if s.Name() == step {
				return true
			}
		}
	} else {
		for _, s := range WorkloadSteps {
			if s.Name() == step {
				return true
			}
		}
	}
	return false
}

func InvalidSteps(steps []string, controlRoom bool) error {
	for _, step := range steps {
		if !ValidStep(step, controlRoom) {
			return fmt.Errorf("invalid step name %s", step)
		}
	}
	return nil
}

func OnlySteps(allSteps []Step, onlySteps []string) (stepsToRun []Step, err error) {
	// if the user has not passed in any steps, run all steps
	if len(onlySteps) == 0 {
		return allSteps, nil
	}

	for _, step := range allSteps {
		if slices.Contains(onlySteps, step.Name()) {
			stepsToRun = append(stepsToRun, step)
		}
	}

	return
}

func StartAtStep(allSteps []Step, startAtStep string) ([]Step, error) {
	// if the user has not passed in any steps, run all steps
	if startAtStep == "" {
		return allSteps, nil
	}

	start := -1
	for i, step := range allSteps {
		if step.Name() == startAtStep {
			start = i
			break
		}
	}

	if start == -1 {
		return nil, fmt.Errorf("step %s not in steps to run list: %s", startAtStep, Names(allSteps))
	}

	return allSteps[start:], nil
}

type StepOptions struct {
	DryRun            bool
	Refresh           bool
	Cancel            bool
	Preview           bool
	AutoApply         bool
	Destroy           bool
	ExcludedResources []string
	TargetResources   []string
}

type Step interface {
	Run(ctx context.Context) error
	Set(t types.Target, controlRoomTarget types.Target, options StepOptions)
	Name() string
	ProxyRequired() bool
}

func ProxyRequiredSteps(steps []Step) bool {
	for _, step := range steps {
		if step.ProxyRequired() {
			return true
		}
	}
	return false
}

func pulumiRefreshPreviewUpCancel(ctx context.Context, stack auto.Stack, options StepOptions) error {
	// output pulumi whoami details every time
	err := pulumi.Whoami(ctx, stack)
	if err != nil {
		return err
	}

	// cancel is a once-and-done, exit at the conclusion.
	if options.Cancel {
		return pulumi.CancelStack(ctx, stack)
	}

	if options.Refresh && options.DryRun {
		return fmt.Errorf("refresh and dryRun are mutually exclusive")
	}

	// perform refresh separately from preview/up
	if options.Refresh {
		_, err := pulumi.RefreshStack(ctx, stack)
		if err != nil {
			return err
		}
	}

	// handle destroy operation
	if options.Destroy {
		_, err = pulumi.DestroyStack(ctx, stack, options.Preview, options.DryRun, options.ExcludedResources, options.TargetResources)
		if err != nil {
			return err
		}
	} else {
		// delegate remaining preview/up decisions to pulumi.UpStack
		_, err = pulumi.UpStack(ctx, stack, options.Preview, options.DryRun, options.AutoApply, options.ExcludedResources, options.TargetResources)
		if err != nil {
			return err
		}
	}

	return nil
}

// prepareEnvVarsForPulumi prepares environment variables for Pulumi stack creation.
func prepareEnvVarsForPulumi(ctx context.Context, target types.Target, creds types.Credentials) (map[string]string, error) {
	envVars := make(map[string]string)
	for k, v := range creds.EnvVars() {
		envVars[k] = v
	}
	return envVars, nil
}

func setLoggerWithContext(t types.Target, controlRoomTarget types.Target, options StepOptions, stepName string) *slog.Logger {
	l := slog.With(
		"step_name", stepName,
		"target", t.Name(),
		"dry_run", options.DryRun,
	)

	if controlRoomTarget != nil {
		l = l.With("control_room", controlRoomTarget.Name())
	}

	return l
}
