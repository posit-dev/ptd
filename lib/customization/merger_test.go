package customization

import (
	"testing"
)

// mockStepInfo implements the StepInfo interface for testing
type mockStepInfo struct {
	name string
}

func (m *mockStepInfo) Name() string {
	return m.name
}

func TestMergeSteps_InsertAfter(t *testing.T) {
	// Setup standard steps
	standardSteps := []StepInfo{
		&mockStepInfo{"bootstrap"},
		&mockStepInfo{"persistent"},
		&mockStepInfo{"eks"},
		&mockStepInfo{"helm"},
	}

	// Setup custom step to insert after "persistent"
	customStep := &mockStepInfo{"custom-step"}
	customSteps := []StepInfo{customStep}

	insertionPlans := map[string]InsertionPlan{
		"custom-step": {
			CustomStep:  customStep,
			InsertAfter: "persistent",
		},
	}

	result, err := MergeSteps(standardSteps, customSteps, insertionPlans)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"bootstrap", "persistent", "custom-step", "eks", "helm"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d steps, got %d", len(expected), len(result))
	}

	for i, step := range result {
		if step.Name() != expected[i] {
			t.Errorf("step %d: expected %s, got %s", i, expected[i], step.Name())
		}
	}
}

func TestMergeSteps_InsertBefore(t *testing.T) {
	standardSteps := []StepInfo{
		&mockStepInfo{"bootstrap"},
		&mockStepInfo{"persistent"},
		&mockStepInfo{"eks"},
		&mockStepInfo{"helm"},
	}

	customStep := &mockStepInfo{"custom-step"}
	customSteps := []StepInfo{customStep}

	insertionPlans := map[string]InsertionPlan{
		"custom-step": {
			CustomStep:   customStep,
			InsertBefore: "eks",
		},
	}

	result, err := MergeSteps(standardSteps, customSteps, insertionPlans)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"bootstrap", "persistent", "custom-step", "eks", "helm"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d steps, got %d", len(expected), len(result))
	}

	for i, step := range result {
		if step.Name() != expected[i] {
			t.Errorf("step %d: expected %s, got %s", i, expected[i], step.Name())
		}
	}
}

func TestMergeSteps_InsertAtEnd(t *testing.T) {
	standardSteps := []StepInfo{
		&mockStepInfo{"bootstrap"},
		&mockStepInfo{"persistent"},
		&mockStepInfo{"eks"},
	}

	customStep := &mockStepInfo{"custom-step"}
	customSteps := []StepInfo{customStep}

	// No insertAfter or insertBefore - should append at end
	insertionPlans := map[string]InsertionPlan{
		"custom-step": {
			CustomStep: customStep,
		},
	}

	result, err := MergeSteps(standardSteps, customSteps, insertionPlans)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"bootstrap", "persistent", "eks", "custom-step"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d steps, got %d", len(expected), len(result))
	}

	for i, step := range result {
		if step.Name() != expected[i] {
			t.Errorf("step %d: expected %s, got %s", i, expected[i], step.Name())
		}
	}
}

func TestMergeSteps_MultipleCustomSteps(t *testing.T) {
	standardSteps := []StepInfo{
		&mockStepInfo{"bootstrap"},
		&mockStepInfo{"persistent"},
		&mockStepInfo{"eks"},
		&mockStepInfo{"helm"},
		&mockStepInfo{"sites"},
	}

	custom1 := &mockStepInfo{"custom-dns"}
	custom2 := &mockStepInfo{"custom-monitoring"}
	custom3 := &mockStepInfo{"custom-cleanup"}

	customSteps := []StepInfo{custom1, custom2, custom3}

	insertionPlans := map[string]InsertionPlan{
		"custom-dns": {
			CustomStep:  custom1,
			InsertAfter: "persistent",
		},
		"custom-monitoring": {
			CustomStep:  custom2,
			InsertAfter: "helm",
		},
		"custom-cleanup": {
			CustomStep: custom3,
			// No insertion point - will append at end
		},
	}

	result, err := MergeSteps(standardSteps, customSteps, insertionPlans)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected order: bootstrap, persistent, custom-dns, eks, helm, custom-monitoring, sites, custom-cleanup
	expected := []string{
		"bootstrap",
		"persistent",
		"custom-dns",
		"eks",
		"helm",
		"custom-monitoring",
		"sites",
		"custom-cleanup",
	}

	if len(result) != len(expected) {
		t.Fatalf("expected %d steps, got %d", len(expected), len(result))
	}

	for i, step := range result {
		if step.Name() != expected[i] {
			t.Errorf("step %d: expected %s, got %s", i, expected[i], step.Name())
		}
	}
}

func TestMergeSteps_NoCustomSteps(t *testing.T) {
	standardSteps := []StepInfo{
		&mockStepInfo{"bootstrap"},
		&mockStepInfo{"persistent"},
		&mockStepInfo{"eks"},
	}

	customSteps := []StepInfo{}
	insertionPlans := map[string]InsertionPlan{}

	result, err := MergeSteps(standardSteps, customSteps, insertionPlans)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != len(standardSteps) {
		t.Fatalf("expected %d steps, got %d", len(standardSteps), len(result))
	}

	for i, step := range result {
		if step.Name() != standardSteps[i].Name() {
			t.Errorf("step %d: expected %s, got %s", i, standardSteps[i].Name(), step.Name())
		}
	}
}

func TestMergeSteps_InsertAfterLastStep(t *testing.T) {
	standardSteps := []StepInfo{
		&mockStepInfo{"bootstrap"},
		&mockStepInfo{"persistent"},
		&mockStepInfo{"eks"},
	}

	customStep := &mockStepInfo{"custom-step"}
	customSteps := []StepInfo{customStep}

	insertionPlans := map[string]InsertionPlan{
		"custom-step": {
			CustomStep:  customStep,
			InsertAfter: "eks",
		},
	}

	result, err := MergeSteps(standardSteps, customSteps, insertionPlans)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected order: bootstrap, persistent, eks, custom-step
	expected := []string{"bootstrap", "persistent", "eks", "custom-step"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d steps, got %d", len(expected), len(result))
	}

	for i, step := range result {
		if step.Name() != expected[i] {
			t.Errorf("step %d: expected %s, got %s", i, expected[i], step.Name())
		}
	}
}

func TestMergeSteps_InsertBeforeFirstStep(t *testing.T) {
	standardSteps := []StepInfo{
		&mockStepInfo{"bootstrap"},
		&mockStepInfo{"persistent"},
		&mockStepInfo{"eks"},
	}

	customStep := &mockStepInfo{"custom-step"}
	customSteps := []StepInfo{customStep}

	insertionPlans := map[string]InsertionPlan{
		"custom-step": {
			CustomStep:   customStep,
			InsertBefore: "bootstrap",
		},
	}

	result, err := MergeSteps(standardSteps, customSteps, insertionPlans)

	// Since there's no step before bootstrap, this should fail
	// The implementation can't insert before the first step
	if err == nil {
		t.Error("expected error when trying to insert before first step, but got none")
	}

	// Verify the error message contains relevant info
	if err != nil && !contains(err.Error(), "could not be inserted") {
		t.Errorf("expected error about insertion failure, got: %v", err)
	}

	// Result should be nil or empty when there's an error
	_ = result
}

func TestMergeSteps_MultipleStepsSameInsertionPoint(t *testing.T) {
	standardSteps := []StepInfo{
		&mockStepInfo{"bootstrap"},
		&mockStepInfo{"persistent"},
		&mockStepInfo{"eks"},
	}

	custom1 := &mockStepInfo{"custom-step-1"}
	custom2 := &mockStepInfo{"custom-step-2"}

	customSteps := []StepInfo{custom1, custom2}

	// Both want to insert after persistent
	insertionPlans := map[string]InsertionPlan{
		"custom-step-1": {
			CustomStep:  custom1,
			InsertAfter: "persistent",
		},
		"custom-step-2": {
			CustomStep:  custom2,
			InsertAfter: "persistent",
		},
	}

	result, err := MergeSteps(standardSteps, customSteps, insertionPlans)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both should be inserted after persistent
	// The order between custom-step-1 and custom-step-2 depends on map iteration
	// but both should be between persistent and eks

	persistentIdx := -1
	eksIdx := -1
	custom1Idx := -1
	custom2Idx := -1

	for i, step := range result {
		switch step.Name() {
		case "persistent":
			persistentIdx = i
		case "eks":
			eksIdx = i
		case "custom-step-1":
			custom1Idx = i
		case "custom-step-2":
			custom2Idx = i
		}
	}

	if persistentIdx == -1 || eksIdx == -1 || custom1Idx == -1 || custom2Idx == -1 {
		t.Fatal("not all steps found in result")
	}

	if custom1Idx <= persistentIdx || custom1Idx >= eksIdx {
		t.Errorf("custom-step-1 not between persistent and eks")
	}

	if custom2Idx <= persistentIdx || custom2Idx >= eksIdx {
		t.Errorf("custom-step-2 not between persistent and eks")
	}
}

func TestGetStandardStepNames(t *testing.T) {
	steps := []StepInfo{
		&mockStepInfo{"bootstrap"},
		&mockStepInfo{"persistent"},
		&mockStepInfo{"eks"},
	}

	names := GetStandardStepNames(steps)

	expected := []string{"bootstrap", "persistent", "eks"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d names, got %d", len(expected), len(names))
	}

	for i, name := range names {
		if name != expected[i] {
			t.Errorf("name %d: expected %s, got %s", i, expected[i], name)
		}
	}
}

func TestVerifyInsertionPoint(t *testing.T) {
	standardStepNames := []string{"bootstrap", "persistent", "eks", "helm"}

	tests := []struct {
		name        string
		insertPoint string
		expectedOK  bool
	}{
		{"valid step name", "persistent", true},
		{"invalid step name", "nonexistent", false},
		{"empty string", "", true},
		{"first step", "bootstrap", true},
		{"last step", "helm", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok := VerifyInsertionPoint(tt.insertPoint, standardStepNames)
			if ok != tt.expectedOK {
				t.Errorf("VerifyInsertionPoint(%q) = %v, expected %v", tt.insertPoint, ok, tt.expectedOK)
			}
		})
	}
}

func TestBuildInsertionPlans(t *testing.T) {
	enabled := true
	disabled := false

	customSteps := []CustomStep{
		{
			Name:        "step1",
			Path:        "step1/",
			InsertAfter: "persistent",
			Enabled:     &enabled,
		},
		{
			Name:         "step2",
			Path:         "step2/",
			InsertBefore: "eks",
			Enabled:      &enabled,
		},
		{
			Name:    "step3",
			Path:    "step3/",
			Enabled: &disabled, // This should be skipped
		},
		{
			Name:    "step4",
			Path:    "step4/",
			Enabled: nil, // nil means enabled by default
		},
	}

	stepInfos := []StepInfo{
		&mockStepInfo{"step1"},
		&mockStepInfo{"step2"},
		&mockStepInfo{"step4"}, // step3 is disabled, so not included
	}

	plans, err := BuildInsertionPlans(customSteps, stepInfos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have plans for step1, step2, and step4 (step3 is disabled)
	if len(plans) != 3 {
		t.Fatalf("expected 3 plans, got %d", len(plans))
	}

	// Verify step1
	if plan, ok := plans["step1"]; !ok {
		t.Error("missing plan for step1")
	} else {
		if plan.InsertAfter != "persistent" {
			t.Errorf("step1 InsertAfter: expected 'persistent', got '%s'", plan.InsertAfter)
		}
	}

	// Verify step2
	if plan, ok := plans["step2"]; !ok {
		t.Error("missing plan for step2")
	} else {
		if plan.InsertBefore != "eks" {
			t.Errorf("step2 InsertBefore: expected 'eks', got '%s'", plan.InsertBefore)
		}
	}

	// Verify step3 is not in plans
	if _, ok := plans["step3"]; ok {
		t.Error("disabled step3 should not be in plans")
	}

	// Verify step4 is in plans (enabled by default)
	if _, ok := plans["step4"]; !ok {
		t.Error("missing plan for step4 (enabled by default)")
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestMergeSteps_EdgeCases(t *testing.T) {
	t.Run("empty standard steps", func(t *testing.T) {
		standardSteps := []StepInfo{}
		customStep := &mockStepInfo{"custom"}
		customSteps := []StepInfo{customStep}
		insertionPlans := map[string]InsertionPlan{
			"custom": {CustomStep: customStep},
		}

		result, err := MergeSteps(standardSteps, customSteps, insertionPlans)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("expected 1 step, got %d", len(result))
		}

		if result[0].Name() != "custom" {
			t.Errorf("expected 'custom', got '%s'", result[0].Name())
		}
	})

	t.Run("both insertAfter and insertBefore", func(t *testing.T) {
		standardSteps := []StepInfo{
			&mockStepInfo{"step1"},
			&mockStepInfo{"step2"},
			&mockStepInfo{"step3"},
		}
		customStep := &mockStepInfo{"custom"}
		customSteps := []StepInfo{customStep}
		insertionPlans := map[string]InsertionPlan{
			"custom": {
				CustomStep:   customStep,
				InsertAfter:  "step1",
				InsertBefore: "step2",
			},
		}

		result, err := MergeSteps(standardSteps, customSteps, insertionPlans)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should insert after step1 (before step2)
		expected := []string{"step1", "custom", "step2", "step3"}
		if len(result) != len(expected) {
			t.Fatalf("expected %d steps, got %d", len(expected), len(result))
		}

		for i, step := range result {
			if step.Name() != expected[i] {
				t.Errorf("step %d: expected %s, got %s", i, expected[i], step.Name())
			}
		}
	})
}
