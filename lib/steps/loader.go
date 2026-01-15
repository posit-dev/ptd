package steps

import (
	"fmt"
	"log/slog"

	"github.com/rstudio/ptd/lib/customization"
)

// LoadStepsForWorkload loads standard steps and merges in custom steps from manifest if present
func LoadStepsForWorkload(workloadPath string, isControlRoom bool) ([]Step, error) {
	// Get standard steps
	var standardSteps []Step
	if isControlRoom {
		standardSteps = ControlRoomSteps
	} else {
		standardSteps = WorkloadSteps
	}

	// Look for customizations manifest
	manifestPath, found := customization.FindManifest(workloadPath)
	if !found {
		slog.Debug("no custom steps manifest found", "workload_path", workloadPath)
		return standardSteps, nil
	}

	slog.Info("loading custom steps manifest", "manifest_path", manifestPath)

	// Parse manifest
	manifest, err := customization.LoadManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load manifest: %w", err)
	}

	// Validate manifest
	standardStepNames := Names(standardSteps)
	if err := manifest.Validate(workloadPath, standardStepNames); err != nil {
		return nil, fmt.Errorf("manifest validation failed: %w", err)
	}

	// Create CustomStep instances
	customSteps := make([]Step, 0, len(manifest.CustomSteps))
	customStepInfos := make([]customization.StepInfo, 0, len(manifest.CustomSteps))

	for _, cs := range manifest.CustomSteps {
		if !cs.IsEnabled() {
			slog.Debug("skipping disabled custom step", "name", cs.Name)
			continue
		}

		customStep := NewCustomStep(cs, workloadPath)
		customSteps = append(customSteps, customStep)
		customStepInfos = append(customStepInfos, customStep)
	}

	if len(customSteps) == 0 {
		slog.Debug("no enabled custom steps found")
		return standardSteps, nil
	}

	// Build insertion plans
	insertionPlans, err := buildInsertionPlansFromManifest(manifest.CustomSteps, customStepInfos)
	if err != nil {
		return nil, fmt.Errorf("failed to build insertion plans: %w", err)
	}

	// Convert standard steps to StepInfo interface
	standardStepInfos := make([]customization.StepInfo, len(standardSteps))
	for i, step := range standardSteps {
		standardStepInfos[i] = step
	}

	// Merge custom steps into standard steps
	mergedInfos, err := customization.MergeSteps(standardStepInfos, customStepInfos, insertionPlans)
	if err != nil {
		return nil, fmt.Errorf("failed to merge steps: %w", err)
	}

	// Convert back to []Step
	result := make([]Step, len(mergedInfos))
	for i, info := range mergedInfos {
		result[i] = info.(Step)
	}

	slog.Info("loaded steps with custom steps", "total_steps", len(result), "custom_steps", len(customSteps))
	return result, nil
}

// buildInsertionPlansFromManifest creates insertion plans from manifest custom steps
func buildInsertionPlansFromManifest(customSteps []customization.CustomStep, stepInfos []customization.StepInfo) (map[string]customization.InsertionPlan, error) {
	plans := make(map[string]customization.InsertionPlan)

	enabledIdx := 0
	for _, cs := range customSteps {
		if !cs.IsEnabled() {
			continue
		}

		plans[cs.Name] = customization.InsertionPlan{
			CustomStep:   stepInfos[enabledIdx],
			InsertAfter:  cs.InsertAfter,
			InsertBefore: cs.InsertBefore,
		}
		enabledIdx++
	}

	return plans, nil
}

// IsCustomStep checks if a step is a custom step
func IsCustomStep(step Step) bool {
	_, ok := step.(*CustomStep)
	return ok
}
