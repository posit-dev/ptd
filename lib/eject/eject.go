package eject

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/posit-dev/ptd/lib/types"
)

// Options holds configuration for the eject command
type Options struct {
	TargetName string
	OutputDir  string
	DryRun     bool
}

// Run orchestrates the eject artifact bundle generation
func Run(ctx context.Context, t types.Target, opts Options) error {
	slog.Info("Starting eject", "target", opts.TargetName, "output-dir", opts.OutputDir, "dry-run", opts.DryRun)

	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	slog.Info("Created output directory", "path", opts.OutputDir)
	return nil
}
