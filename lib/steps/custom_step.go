package steps

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/posit-dev/ptd/lib/customization"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/pulumi"
	"github.com/posit-dev/ptd/lib/types"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
)

// CustomStep represents a custom Go-based Pulumi step defined in a workload's customizations directory
type CustomStep struct {
	name          string
	description   string
	programPath   string // Absolute path to custom step directory
	proxyRequired bool
	target        types.Target
	controlRoom   types.Target
	options       StepOptions
	logger        *slog.Logger
}

// NewCustomStep creates a new CustomStep from a manifest custom step definition
func NewCustomStep(cs customization.CustomStep, workloadPath string) *CustomStep {
	return &CustomStep{
		name:          cs.Name,
		description:   cs.Description,
		programPath:   filepath.Join(workloadPath, "customizations", cs.Path),
		proxyRequired: cs.ProxyRequired,
	}
}

// Name returns the step name
func (s *CustomStep) Name() string {
	return s.name
}

// ProxyRequired indicates if this step requires a proxy connection
func (s *CustomStep) ProxyRequired() bool {
	return s.proxyRequired
}

// Set configures the step with target and options
func (s *CustomStep) Set(t types.Target, controlRoomTarget types.Target, options StepOptions) {
	s.target = t
	s.controlRoom = controlRoomTarget
	s.options = options
	s.logger = setLoggerWithContext(t, controlRoomTarget, options, s.name)
}

// Run executes the custom step
func (s *CustomStep) Run(ctx context.Context) error {
	s.logger.Info("running custom step", "path", s.programPath, "description", s.description)

	// Validate step structure before running
	if err := s.Validate(); err != nil {
		return fmt.Errorf("custom step validation failed: %w", err)
	}

	// Get credentials for the target
	creds, err := s.target.Credentials(ctx)
	if err != nil {
		return fmt.Errorf("failed to get credentials: %w", err)
	}
	envVars := creds.EnvVars()

	// Create stack based on local source defined in program path
	stack, err := pulumi.LocalStack(
		ctx,
		s.target,
		s.programPath,
		s.name,
		envVars,
	)

	if err != nil {
		return fmt.Errorf("failed to create custom step stack: %w", err)
	}

	// Set stack config values used in custom steps
	if err := stack.SetConfig(ctx, "ptd:workloadName", auto.ConfigValue{Value: s.target.Name()}); err != nil {
		return err
	}

	yamlPath := helpers.YamlPathForTarget(s.target)
	if err := stack.SetConfig(ctx, "ptd:ptdYamlPath", auto.ConfigValue{Value: yamlPath}); err != nil {
		return err
	}

	if err := runPulumi(ctx, stack, s.options); err != nil {
		return fmt.Errorf("custom step failed: %w", err)
	}

	s.logger.Info("custom step completed successfully")
	return nil
}

// Validate checks that the custom step directory has required files
func (s *CustomStep) Validate() error {
	cs := customization.CustomStep{
		Name: s.name,
		Path: "", // Path validation happens at manifest load time
	}

	// Validate the program directory structure
	if err := cs.ValidateStepDirectory(s.programPath); err != nil {
		return err
	}

	return nil
}

// Description returns the step description
func (s *CustomStep) Description() string {
	return s.description
}

// ProgramPath returns the absolute path to the custom step directory
func (s *CustomStep) ProgramPath() string {
	return s.programPath
}
