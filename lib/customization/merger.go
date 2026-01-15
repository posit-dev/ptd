package customization

import (
	"fmt"
	"slices"
)

// StepInfo is a minimal interface that custom and standard steps must implement
// This allows the merger to work with any step type
type StepInfo interface {
	Name() string
}

// InsertionPlan describes where a custom step should be inserted
type InsertionPlan struct {
	CustomStep   StepInfo
	InsertAfter  string
	InsertBefore string
}

// MergeSteps combines standard steps with custom steps according to their insertion points
// This function is generic over the step type to allow reuse
func MergeSteps(standardSteps []StepInfo, customSteps []StepInfo, insertionPlans map[string]InsertionPlan) ([]StepInfo, error) {
	if len(customSteps) == 0 {
		return standardSteps, nil
	}

	result := make([]StepInfo, 0, len(standardSteps)+len(customSteps))

	// Track which custom steps have been inserted
	inserted := make(map[string]bool)

	// First pass: insert custom steps with explicit positions
	for i, standardStep := range standardSteps {
		// Add the standard step
		result = append(result, standardStep)

		// Check if any custom steps should be inserted after this standard step
		for customName, plan := range insertionPlans {
			if inserted[customName] {
				continue
			}

			if plan.InsertAfter == standardStep.Name() {
				// Insert after this step
				result = append(result, plan.CustomStep)
				inserted[customName] = true
			} else if plan.InsertBefore != "" && i+1 < len(standardSteps) && standardSteps[i+1].Name() == plan.InsertBefore {
				// Insert before the next step (which means after current step)
				result = append(result, plan.CustomStep)
				inserted[customName] = true
			}
		}
	}

	// Second pass: append any custom steps without insertion points at the end
	for customName, plan := range insertionPlans {
		if !inserted[customName] {
			if plan.InsertAfter == "" && plan.InsertBefore == "" {
				result = append(result, plan.CustomStep)
				inserted[customName] = true
			} else {
				return nil, fmt.Errorf("custom step %s could not be inserted: insertAfter=%s, insertBefore=%s",
					customName, plan.InsertAfter, plan.InsertBefore)
			}
		}
	}

	return result, nil
}

// BuildInsertionPlans creates a map of insertion plans from the manifest custom steps
func BuildInsertionPlans(customSteps []CustomStep, stepInfos []StepInfo) (map[string]InsertionPlan, error) {
	plans := make(map[string]InsertionPlan)

	stepInfoIdx := 0
	for _, cs := range customSteps {
		if !cs.IsEnabled() {
			continue
		}

		if stepInfoIdx >= len(stepInfos) {
			return nil, fmt.Errorf("mismatch between enabled custom steps and stepInfos")
		}

		plans[cs.Name] = InsertionPlan{
			CustomStep:   stepInfos[stepInfoIdx],
			InsertAfter:  cs.InsertAfter,
			InsertBefore: cs.InsertBefore,
		}
		stepInfoIdx++
	}

	return plans, nil
}

// GetStandardStepNames extracts step names from a slice of steps
func GetStandardStepNames(steps []StepInfo) []string {
	names := make([]string, len(steps))
	for i, step := range steps {
		names[i] = step.Name()
	}
	return names
}

// VerifyInsertionPoint checks if an insertion point is valid
func VerifyInsertionPoint(insertPoint string, standardStepNames []string) bool {
	if insertPoint == "" {
		return true
	}
	return slices.Contains(standardStepNames, insertPoint)
}
