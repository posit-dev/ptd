package eject

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/types"
)

type ConfigLoaderFunc func(types.Target) (interface{}, error)

type Options struct {
	TargetName   string
	OutputDir    string
	DryRun       bool
	CLIVersion   string
	WorkloadPath string
	ConfigLoader ConfigLoaderFunc // nil defaults to helpers.ConfigForTarget
}

type Bundle struct {
	ControlRoom *ControlRoomDetails `json:"control_room"`
}

func (o *Options) configLoader() ConfigLoaderFunc {
	if o.ConfigLoader != nil {
		return o.ConfigLoader
	}
	return helpers.ConfigForTarget
}

func Run(ctx context.Context, t types.Target, opts Options) error {
	slog.Info("Starting eject", "target", opts.TargetName, "output-dir", opts.OutputDir, "dry-run", opts.DryRun)

	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// TODO: serialize bundle to disk once all steps (inventory, secrets, state export) are wired
	bundle := &Bundle{}

	config, err := opts.configLoader()(t)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	crDetails, err := CollectControlRoomDetails(config, t.Name())
	if err != nil {
		return fmt.Errorf("failed to collect control room details: %w", err)
	}
	bundle.ControlRoom = crDetails
	slog.Info("Collected control room details",
		"account_id", crDetails.AccountID,
		"domain", crDetails.Domain,
		"connections", len(crDetails.Connections),
	)

	if opts.WorkloadPath != "" {
		if err := CopyWorkloadConfig(opts.WorkloadPath, opts.OutputDir); err != nil {
			return fmt.Errorf("failed to copy workload config: %w", err)
		}
		slog.Info("Copied workload config", "from", opts.WorkloadPath)
	}

	metadata, err := CollectMetadata(config, opts, time.Now())
	if err != nil {
		return fmt.Errorf("failed to collect metadata: %w", err)
	}
	if err := WriteMetadata(metadata, opts.OutputDir); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}
	slog.Info("Wrote metadata.json")

	hasConfig := opts.WorkloadPath != ""
	if err := WriteReadme(metadata, hasConfig, opts.OutputDir); err != nil {
		return fmt.Errorf("failed to write README: %w", err)
	}
	slog.Info("Wrote README.md")

	slog.Info("Eject bundle generated", "path", opts.OutputDir)
	return nil
}
