package customization

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Validate validates the manifest against the standard steps and file system
func (m *Manifest) Validate(workloadPath string, standardStepNames []string) error {
	// 1. Version must be 1
	if m.Version != 1 {
		return fmt.Errorf("unsupported manifest version %d, expected 1", m.Version)
	}

	// 2. All step names must be unique
	stepNames := make(map[string]bool)
	for _, step := range m.CustomSteps {
		if stepNames[step.Name] {
			return fmt.Errorf("duplicate custom step name: %s", step.Name)
		}
		stepNames[step.Name] = true
	}

	// 3. Validate each custom step
	for _, step := range m.CustomSteps {
		if err := step.Validate(workloadPath, standardStepNames); err != nil {
			return fmt.Errorf("custom step %s: %w", step.Name, err)
		}
	}

	// 4. Validate insertion points are not conflicting
	if err := m.validateInsertionPoints(standardStepNames); err != nil {
		return err
	}

	return nil
}

// Validate validates a single custom step
func (cs *CustomStep) Validate(workloadPath string, standardStepNames []string) error {
	// Name is required
	if cs.Name == "" {
		return fmt.Errorf("custom step name is required")
	}

	// Path is required
	if cs.Path == "" {
		return fmt.Errorf("custom step %q: path is required", cs.Name)
	}

	// Validate insertion points
	if cs.InsertAfter != "" && !slices.Contains(standardStepNames, cs.InsertAfter) {
		return fmt.Errorf("insertAfter references unknown step: %s", cs.InsertAfter)
	}
	if cs.InsertBefore != "" && !slices.Contains(standardStepNames, cs.InsertBefore) {
		return fmt.Errorf("insertBefore references unknown step: %s", cs.InsertBefore)
	}

	// If both insertAfter and insertBefore are specified, verify they are adjacent
	if cs.InsertAfter != "" && cs.InsertBefore != "" {
		afterIdx := slices.Index(standardStepNames, cs.InsertAfter)
		beforeIdx := slices.Index(standardStepNames, cs.InsertBefore)

		if afterIdx == -1 || beforeIdx == -1 {
			return fmt.Errorf("insertAfter or insertBefore references unknown step")
		}

		// Check if they are adjacent
		if beforeIdx != afterIdx+1 {
			return fmt.Errorf("insertAfter '%s' and insertBefore '%s' are not adjacent steps", cs.InsertAfter, cs.InsertBefore)
		}
	}

	// Validate the step directory exists and has required files
	stepPath := filepath.Join(workloadPath, "customizations", cs.Path)
	if err := cs.ValidateStepDirectory(stepPath); err != nil {
		return err
	}

	return nil
}

// ValidateStepDirectory checks that the custom step directory has required files
func (cs *CustomStep) ValidateStepDirectory(stepPath string) error {
	// Check directory exists
	info, err := os.Stat(stepPath)
	if err != nil {
		return fmt.Errorf("step directory does not exist: %s (check that path %q in manifest is correct)", stepPath, cs.Path)
	}
	if !info.IsDir() {
		return fmt.Errorf("step path is not a directory: %s", stepPath)
	}

	// Check main.go exists
	mainFile := filepath.Join(stepPath, "main.go")
	if _, err := os.Stat(mainFile); err != nil {
		return fmt.Errorf("main.go not found in step directory: %s", stepPath)
	}

	// Check go.mod exists
	modFile := filepath.Join(stepPath, "go.mod")
	if _, err := os.Stat(modFile); err != nil {
		return fmt.Errorf("go.mod not found in step directory: %s", stepPath)
	}

	return nil
}

// validateInsertionPoints ensures insertion points are consistent
func (m *Manifest) validateInsertionPoints(standardStepNames []string) error {
	// Check if both insertAfter and insertBefore are specified for any step
	for _, step := range m.CustomSteps {
		if step.InsertAfter != "" && step.InsertBefore != "" {
			// Both specified - need to verify they are adjacent in standard steps
			afterIdx := slices.Index(standardStepNames, step.InsertAfter)
			beforeIdx := slices.Index(standardStepNames, step.InsertBefore)

			if afterIdx == -1 || beforeIdx == -1 {
				return fmt.Errorf("custom step %s: insertAfter or insertBefore references unknown step", step.Name)
			}

			// Check if they are adjacent
			if beforeIdx != afterIdx+1 {
				return fmt.Errorf("custom step %s: insertAfter '%s' and insertBefore '%s' are not adjacent steps",
					step.Name, step.InsertAfter, step.InsertBefore)
			}
		}
	}

	return nil
}
